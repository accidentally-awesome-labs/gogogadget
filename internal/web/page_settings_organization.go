package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /app/settings/org
//
// Same shape as the account page, including why the refusal is not logged:
// the "manage organization" link is the selected identity adapter's own
// organization page, and an adapter that keeps organizations in this
// application's own tables refuses, leaving the member list and the export
// card intact and saying so in the caption.
func (s *Server) handleSettingsOrg(w http.ResponseWriter, r *http.Request) {
	org := identity.OrgFrom(r.Context())
	members, err := s.q.ListMembersByOrg(r.Context(), org.OrgID)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	organizationURL, _ := s.navigator.OrganizationURL(s.cfg.AppURL + r.URL.Path)
	s.Render(w, r, Page{Title: "Organization settings", Layout: templates.LayoutApp},
		templates.SettingsOrg(*org, members, organizationURL, isOrgAdmin(r)))
}
