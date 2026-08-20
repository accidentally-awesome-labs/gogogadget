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
	// The admin must STILL hold the full role: demotion mid-session — to
	// 'support' or to nothing — kills the impersonation.
	admin, err := s.q.GetUserByClerkID(ctx, sess.AdminUserID)
	if err != nil || !identity.IsAdmin(&admin) {
		clear()
		return ctx
	}
	target, err := s.q.GetUserByClerkID(ctx, sess.TargetUserID)
	if err != nil {
		clear()
		return ctx
	}
	org, err := s.q.GetOrgByClerkID(ctx, sess.TargetOrgID)
	if err != nil {
		clear()
		return ctx
	}
	m, err := s.q.GetMembership(ctx, sqlc.GetMembershipParams{ClerkOrgID: sess.TargetOrgID, ClerkUserID: sess.TargetUserID})
	if err != nil {
		clear()
		return ctx
	}

	// Rebuild identity as the target; downstream guards are unchanged.
	ctx = identity.WithUser(ctx, &target)
	ctx = identity.WithOrg(ctx, &org)
	ctx = identity.WithClaims(ctx, &identity.Claims{
		UserID: target.ClerkUserID, OrgID: org.ClerkOrgID, OrgRole: m.Role, OrgSlug: org.Slug,
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
	target, err := s.q.GetUserByClerkID(ctx, targetID)
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
	orgs, err := s.q.GetOrgsForUser(ctx, target.ClerkUserID)
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

	target, err := s.q.GetUserByClerkID(ctx, targetID)
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
		orgID = orgs[0].ClerkOrgID
	}
	// The target must actually be a member of the chosen org.
	if _, err := s.q.GetMembership(ctx, sqlc.GetMembershipParams{ClerkOrgID: orgID, ClerkUserID: targetID}); err != nil {
		http.Error(w, "target is not a member of that organization", http.StatusUnprocessableEntity)
		return
	}

	sess, err := s.q.InsertImpersonationSession(ctx, sqlc.InsertImpersonationSessionParams{
		ID: newImpersonationID(), AdminUserID: admin.ClerkUserID,
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
	audit.Log(ctx, s.q, orgID, admin.ClerkUserID, "impersonation.start", map[string]any{
		"target_user_id": targetID, "target_org_id": orgID, "session_id": sess.ID, "reason": reason,
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
		meta := map[string]any{"session_id": imp.SessionID}
		if sess, err := s.q.GetImpersonationSession(ctx, imp.SessionID); err == nil {
			meta["reason"] = sess.Reason
			meta["target_user_id"] = sess.TargetUserID
		}
		audit.Log(ctx, s.q, imp.SessionID, imp.AdminUserID, "impersonation.stop", meta)
	}
	http.SetCookie(w, &http.Cookie{Name: impersonationCookieName, Value: "", Path: "/", MaxAge: -1})
	Redirect(w, r, "/admin")
}
