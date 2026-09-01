package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/jackc/pgx/v5/pgtype"
)

// Impersonation: an admin "views as" a target user. The session row carries
// admin + target + org; the ggg_imp cookie holds the opaque session id.
// sessionLoad applies the override AFTER the real JWT verify and mirror
// upsert — impersonation never bypasses Clerk: the admin's own session must
// stay valid, and requireAdmin correctly 403s /admin while impersonating
// (the impersonated target is not a site admin).
const impersonationCookieName = "ggg_imp"

func newImpersonationID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// applyImpersonation swaps the identity context to the target when a valid
// session cookie is present. Any validation failure clears the cookie and
// continues as the admin. Call AFTER the mirror upsert in sessionLoad.
func (s *Server) applyImpersonation(w http.ResponseWriter, r *http.Request, ctx context.Context) context.Context {
	cookie, err := r.Cookie(impersonationCookieName)
	if err != nil || cookie.Value == "" {
		return ctx
	}
	clear := func() {
		http.SetCookie(w, &http.Cookie{Name: impersonationCookieName, Value: "", Path: "/", MaxAge: -1})
	}
	sess, err := s.q.GetImpersonationSession(ctx, cookie.Value)
	if err != nil || sess.EndedAt.Valid || !sess.ExpiresAt.Valid || sess.ExpiresAt.Time.Before(time.Now()) {
		clear()
		return ctx
	}
	// The session must belong to the caller presenting it. Without this the
	// cookie is a pure bearer token: every other check below validates
	// properties of the session ROW, so possession of the id would be the whole
	// authorization, and anyone who learned it would become the target.
	//
	// The id is not secret enough for that. It is an opaque database id, it
	// travels in a cookie that a user can set in their own browser, and it lives
	// for two hours. HttpOnly stops a script reading the admin's copy; it does
	// nothing to stop a third party setting their own.
	//
	// The caller's identity here is the one sessionLoad already verified from the
	// Clerk session, so it cannot be forged alongside the cookie.
	caller := identity.ClaimsFrom(ctx)
	if caller == nil || caller.UserID != sess.AdminUserID {
		clear()
		return ctx
	}
	// The admin must STILL hold the full role: demotion mid-session — to
	// 'support' or to nothing — kills the impersonation.
	admin, err := s.q.GetUserByID(ctx, sess.AdminUserID)
	if err != nil || !identity.IsAdmin(&admin) {
		clear()
		return ctx
	}
	target, err := s.q.GetUserByID(ctx, sess.TargetUserID)
	if err != nil {
		clear()
		return ctx
	}
	org, err := s.q.GetOrgByID(ctx, sess.TargetOrgID)
	if err != nil {
		clear()
		return ctx
	}
	m, err := s.q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: sess.TargetOrgID, UserID: sess.TargetUserID})
	if err != nil {
		clear()
		return ctx
	}

	// Rebuild identity as the target; downstream guards are unchanged.
	ctx = identity.WithUser(ctx, &target)
	ctx = identity.WithOrg(ctx, &org)
	ctx = identity.WithClaims(ctx, &identity.Claims{
		UserID: target.UserID, OrgID: org.OrgID, OrgRole: m.Role, OrgSlug: org.Slug,
	})
	ctx = identity.WithImpersonator(ctx, identity.Impersonator{AdminUserID: sess.AdminUserID, SessionID: sess.ID})
	return ctx
}

// POST /admin/users/{id}/impersonate (adminChain). Org = the optional `org`
// form field, else the target's first membership. Disabled targets and
// org-less targets are rejected.
// Reason capture: impersonation is a deliberate act, not a one-click one.
// The admin states WHY on an interstitial; the reason lands on the session
// row and in both audit entries. Minimum length keeps "test" out of the
// compliance trail.
const (
	impersonationReasonMin = 10
	impersonationReasonMax = 280
)

// GET /admin/users/{id}/impersonate — interstitial: pick the org, state the
// reason, see what will be recorded before starting.
func (s *Server) handleAdminImpersonateForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	targetID := r.PathValue("id")
	target, err := s.q.GetUserByID(ctx, targetID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	orgs, err := s.q.GetOrgsForUser(ctx, targetID)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.Render(w, r, Page{Title: i18n.T(ctx, "impersonation.start_title"), Layout: templates.LayoutAdmin},
		templates.AdminImpersonateForm(templates.ImpersonateData{Target: target, Orgs: orgs}))
}

// renderImpersonateFormError re-renders the interstitial with 422 so the
// stated reason survives the round trip (project-form convention).
func (s *Server) renderImpersonateFormError(w http.ResponseWriter, r *http.Request, target sqlc.User, reason, msg string) {
	ctx := r.Context()
	orgs, err := s.q.GetOrgsForUser(ctx, target.UserID)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	w.WriteHeader(http.StatusUnprocessableEntity)
	s.Render(w, r, Page{Title: i18n.T(ctx, "impersonation.start_title"), Layout: templates.LayoutAdmin},
		templates.AdminImpersonateForm(templates.ImpersonateData{Target: target, Orgs: orgs, Reason: reason, Err: msg}))
}

func (s *Server) handleAdminImpersonate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin := identity.UserFrom(ctx)
	targetID := r.PathValue("id")

	target, err := s.q.GetUserByID(ctx, targetID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if target.DisabledAt.Valid {
		http.Error(w, "cannot impersonate a disabled account", http.StatusUnprocessableEntity)
		return
	}

	reason := strings.TrimSpace(r.FormValue("reason"))
	if len(reason) < impersonationReasonMin || len(reason) > impersonationReasonMax {
		s.renderImpersonateFormError(w, r, target, reason, i18n.T(ctx, "impersonation.reason_invalid"))
		return
	}

	orgID := r.FormValue("org")
	if orgID == "" {
		orgs, err := s.q.GetOrgsForUser(ctx, targetID)
		if err != nil {
			s.renderError(w, r, err.Error())
			return
		}
		if len(orgs) == 0 {
			http.Error(w, "target has no organization", http.StatusUnprocessableEntity)
			return
		}
		orgID = orgs[0].OrgID
	}
	// The target must actually be a member of the chosen org.
	if _, err := s.q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: orgID, UserID: targetID}); err != nil {
		http.Error(w, "target is not a member of that organization", http.StatusUnprocessableEntity)
		return
	}

	sess, err := s.q.InsertImpersonationSession(ctx, sqlc.InsertImpersonationSessionParams{
		ID: newImpersonationID(), AdminUserID: admin.UserID,
		TargetUserID: targetID, TargetOrgID: orgID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(2 * time.Hour), Valid: true},
		Reason:    reason,
	})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: impersonationCookieName, Value: sess.ID, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.cfg.Production(),
		MaxAge: 2 * 60 * 60,
	})
	audit.Log(ctx, s.q, orgID, admin.UserID, "impersonation.start", map[string]any{
		// The session id is deliberately absent. It is a live credential for the
		// duration of the impersonation, and this entry is org-scoped - every
		// member of the target organization reads it on /app/activity, where the
		// full metadata blob is rendered. Recording that an impersonation
		// happened is the audit requirement; handing out the credential is not.
		"target_user_id": targetID, "target_org_id": orgID, "reason": reason,
	})
	// HARD redirect: the banner and the org switcher both live in the shell,
	// which a soft Navigate never re-renders — the target view must boot fresh.
	Redirect(w, r, "/app")
}

// POST /app/impersonation/exit — end the session, clear the cookie, hard
// redirect back to /admin (the shell changes wholesale: hard nav).
func (s *Server) handleImpersonationExit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	imp := identity.ImpersonatorFrom(ctx)
	if imp != nil {
		if err := s.q.EndImpersonationSession(ctx, imp.SessionID); err != nil {
			s.log.Error("impersonation end", "error", err)
		}
		// The org id is what makes an entry findable: /app/activity and
		// RecentAuditByOrg both filter on it. Passing the session id here put
		// the stop event in no organization at all, so an org saw its
		// impersonation begin and never end - the trail read as a session still
		// open. And the session id does not belong in the metadata either, for
		// the same reason it was removed from the start entry.
		meta := map[string]any{}
		orgID := ""
		if sess, err := s.q.GetImpersonationSession(ctx, imp.SessionID); err == nil {
			meta["reason"] = sess.Reason
			meta["target_user_id"] = sess.TargetUserID
			orgID = sess.TargetOrgID
		}
		audit.Log(ctx, s.q, orgID, imp.AdminUserID, "impersonation.stop", meta)
	}
	http.SetCookie(w, &http.Cookie{Name: impersonationCookieName, Value: "", Path: "/", MaxAge: -1})
	Redirect(w, r, "/admin")
}
