package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/jobs"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

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
