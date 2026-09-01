package web

import (
	"net/http"
	"strings"

	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

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

	orgs, err := s.q.GetOrgsForUser(ctx, user.UserID)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}

	// Sole-admin guard: a multi-member org whose only admin leaves would be
	// orphaned. Single-member orgs are deleted wholesale instead.
	var blockers []string
	soleOrgs := make([]string, 0, len(orgs))
	for _, o := range orgs {
		members, err := s.q.CountMembersByOrg(ctx, o.OrgID)
		if err != nil {
			s.renderError(w, r, err.Error())
			return
		}
		if members == 1 {
			soleOrgs = append(soleOrgs, o.OrgID)
			continue
		}
		m, err := s.q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: o.OrgID, UserID: user.UserID})
		if err != nil {
			s.renderError(w, r, err.Error())
			return
		}
		if m.Role == "org:admin" {
			admins, err := s.q.CountAdminsByOrg(ctx, o.OrgID)
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

	// Provider adapters receive upstream subjects, never opaque domain IDs.
	if s.deleter != nil {
		subject, err := s.q.GetIdentitySubjectByUser(ctx, user.UserID)
		if err != nil {
			s.log.Error("identity subject lookup failed", "user", user.UserID, "error", err)
			s.renderError(w, r, err.Error())
			return
		}
		if err := s.deleter.DeleteUser(ctx, subject.Subject); err != nil {
			s.log.Error("identity user delete failed", "user", user.UserID, "error", err)
			s.renderError(w, r, err.Error())
			return
		}
	}

	// Impersonation rows reference users with NO cascade — they must go first.
	if err := s.q.DeleteImpersonationSessionsForUser(ctx, user.UserID); err != nil {
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
	if err := s.q.DeleteUser(ctx, user.UserID); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	// Audit rows deliberately survive (audit_log has no FKs by design).
	audit.Log(ctx, s.q, "", user.UserID, "account.deleted", map[string]any{"orgs_deleted": deleted})

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
