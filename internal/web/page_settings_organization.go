package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /app/settings/org
func (s *Server) handleSettingsOrg(w http.ResponseWriter, r *http.Request) {
	org := identity.OrgFrom(r.Context())
	members, err := s.q.ListMembersByOrg(r.Context(), org.OrgID)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	organizationURL := clerkAccountPortalLink(
		s.cfg.ClerkPortalURL,
		"/organization",
		s.cfg.AppURL+r.URL.Path,
	)
	s.Render(w, r, Page{Title: "Organization settings", Layout: templates.LayoutApp},
		templates.SettingsOrg(*org, members, organizationURL, isOrgAdmin(r)))
}
