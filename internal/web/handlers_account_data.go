package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/jobs"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/jackc/pgx/v5/pgtype"
)

// accountExport is the GDPR self-serve payload: everything the platform
// holds about one user, across orgs.
type accountExport struct {
	ExportedAt    time.Time                 `json:"exported_at"`
	User          sqlc.User                 `json:"user"`
	Memberships   []exportMembership        `json:"memberships"`
	Notifications []sqlc.Notification       `json:"notifications"`
	Audit         []sqlc.ListAuditByUserRow `json:"audit"`
}

type exportMembership struct {
	OrgID   string    `json:"org_id"`
	OrgName string    `json:"org_name"`
	Role    string    `json:"role"`
	Since   time.Time `json:"since"`
}

// GET /app/settings/account/export — JSON download of the user's data.
func (s *Server) handleAccountExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := identity.UserFrom(r.Context())
	org := identity.OrgFrom(r.Context())

	orgs, err := s.q.GetOrgsForUser(ctx, user.ClerkUserID)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	export := accountExport{
		ExportedAt:    s.cfg.Now(),
		User:          *user,
		Memberships:   []exportMembership{},
		Notifications: []sqlc.Notification{},
		Audit:         []sqlc.ListAuditByUserRow{},
	}
	for _, o := range orgs {
		m, err := s.q.GetMembership(ctx, sqlc.GetMembershipParams{ClerkOrgID: o.ClerkOrgID, ClerkUserID: user.ClerkUserID})
		if err != nil {
			s.renderError(w, r, err.Error())
			return
		}
		export.Memberships = append(export.Memberships, exportMembership{
			OrgID: o.ClerkOrgID, OrgName: o.Name, Role: m.Role, Since: m.CreatedAt.Time,
		})
		notes, err := s.q.ListNotificationsByUser(ctx, sqlc.ListNotificationsByUserParams{
			ClerkOrgID: o.ClerkOrgID, ClerkUserID: user.ClerkUserID, Limit: 10000, Offset: 0,
		})
		if err != nil {
			s.renderError(w, r, err.Error())
			return
		}
		export.Notifications = append(export.Notifications, notes...)
	}
	export.Audit, err = s.q.ListAuditByUser(ctx, sqlc.ListAuditByUserParams{
		UserID: pgtype.Text{String: user.ClerkUserID, Valid: true}, Lim: 10000,
	})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}

	body, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	orgID := ""
	if org != nil {
		orgID = org.ClerkOrgID
	}
	audit.Log(ctx, s.q, orgID, user.ClerkUserID, "account.exported", nil)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="gogogadget-data-export.json"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// POST /app/settings/account/delete — GDPR self-serve deletion. Order:
// validate (email match, sole-admin guard) → provider delete → local delete.
func (s *Server) handleAccountDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := identity.UserFrom(r.Context())

	// Never delete while an admin is viewing as this user.
	if identity.ImpersonatorFrom(ctx) != nil {
		w.WriteHeader(http.StatusForbidden)
		s.Render(w, r, Page{Title: i18n.T(ctx, "errors.forbidden"), Layout: templates.LayoutApp}, templates.Forbidden())
		return
	}

	if err := r.ParseForm(); err != nil {
		s.renderAccountFormError(w, r, i18n.T(ctx, "settings.danger_mismatch"))
		return
	}
	if !strings.EqualFold(strings.TrimSpace(r.PostFormValue("confirm_email")), string(user.Email)) {
		s.renderAccountFormError(w, r, i18n.T(ctx, "settings.danger_mismatch"))
		return
	}

	orgs, err := s.q.GetOrgsForUser(ctx, user.ClerkUserID)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}

	// Sole-admin guard: a multi-member org whose only admin leaves would be
	// orphaned. Single-member orgs are deleted wholesale instead.
	var blockers []string
	soleOrgs := make([]string, 0, len(orgs))
	for _, o := range orgs {
		members, err := s.q.CountMembersByOrg(ctx, o.ClerkOrgID)
		if err != nil {
			s.renderError(w, r, err.Error())
			return
		}
		if members == 1 {
			soleOrgs = append(soleOrgs, o.ClerkOrgID)
			continue
		}
		m, err := s.q.GetMembership(ctx, sqlc.GetMembershipParams{ClerkOrgID: o.ClerkOrgID, ClerkUserID: user.ClerkUserID})
		if err != nil {
			s.renderError(w, r, err.Error())
			return
		}
		if m.Role == "org:admin" {
			admins, err := s.q.CountAdminsByOrg(ctx, o.ClerkOrgID)
			if err != nil {
				s.renderError(w, r, err.Error())
				return
			}
			if admins == 1 {
				blockers = append(blockers, o.Name)
			}
		}
	}
	if len(blockers) > 0 {
		s.renderAccountFormError(w, r, i18n.T(ctx, "settings.danger_blocked", strings.Join(blockers, ", ")))
		return
	}

	// Provider first: a failed upstream delete must leave local data intact.
	if s.deleter != nil {
		if err := s.deleter.DeleteUser(ctx, user.ClerkUserID); err != nil {
			s.log.Error("identity user delete failed", "user", user.ClerkUserID, "error", err)
			s.renderError(w, r, err.Error())
			return
		}
	}

	// Impersonation rows reference users with NO cascade — they must go first.
	if err := s.q.DeleteImpersonationSessionsForUser(ctx, user.ClerkUserID); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	// Sole-member orgs die with the account (cascades subscriptions,
	// projects, files, api_tokens, flag_overrides; R2 objects remain —
	// documented in /docs/security).
	deleted := make([]string, 0, len(soleOrgs))
	for _, orgID := range soleOrgs {
		if err := s.q.DeleteOrg(ctx, orgID); err != nil {
			s.renderError(w, r, err.Error())
			return
		}
		deleted = append(deleted, orgID)
	}
	if err := s.q.DeleteUser(ctx, user.ClerkUserID); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	// Audit rows deliberately survive (audit_log has no FKs by design).
	audit.Log(ctx, s.q, "", user.ClerkUserID, "account.deleted", map[string]any{"orgs_deleted": deleted})

	s.clearSessionCookie(w)
	FlashToast(w, "success", i18n.T(ctx, "settings.account_deleted"))
	Redirect(w, r, "/")
}

// renderAccountFormError re-renders the account settings with 422 so the
// danger-zone error shows in place (project-form convention).
func (s *Server) renderAccountFormError(w http.ResponseWriter, r *http.Request, msg string) {
	ctx := r.Context()
	user := identity.UserFrom(r.Context())
	w.WriteHeader(http.StatusUnprocessableEntity)
	s.Render(w, r, Page{Title: i18n.T(ctx, "settings.account_title"), Layout: templates.LayoutApp},
		templates.SettingsDangerZone(*user, msg))
}

// isOrgAdmin reports whether the request's session holds org:admin.
//
// Read from the session claims, which is where the org role lives for every
// other decision in the app — not from the membership row, which the webhook
// mirror can lag behind. A demoted admin's next token refresh removes it.
func isOrgAdmin(r *http.Request) bool {
	claims := identity.ClaimsFrom(r.Context())
	return claims != nil && claims.OrgRole == "org:admin"
}

// POST /app/settings/org/export — enqueue the organization data export.
//
// org:admin only. The payload spans every member's activity, the whole audit
// trail, billing state, and the API/webhook inventory; that is an owner's
// view of the company, not something any member should be able to walk out
// with. (The per-user GDPR export next door stays open to everyone — it only
// contains the caller's own data.)
func (s *Server) handleOrgExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(ctx)
	user := identity.UserFrom(ctx)
	if !isOrgAdmin(r) {
		w.WriteHeader(http.StatusForbidden)
		s.Render(w, r, Page{Title: i18n.T(ctx, "errors.forbidden"), Layout: templates.LayoutApp}, templates.Forbidden())
		return
	}
	if err := jobs.Enqueue(ctx, s.q, jobs.KindExportOrgJSON, jobs.ExportProjectsPayload{
		OrgID: org.ClerkOrgID, UserID: user.ClerkUserID,
	}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	audit.Log(ctx, s.q, org.ClerkOrgID, user.ClerkUserID, "org.exported", nil)
	Toast(w, "success", i18n.T(ctx, "settings.org_export_started"))
	w.WriteHeader(http.StatusOK)
}
