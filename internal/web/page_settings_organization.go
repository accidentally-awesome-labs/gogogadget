package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /app/settings/org
//
// Same shape as the account page: the "manage organization" link is the
// selected identity adapter's own organization page, and an adapter that
// keeps organizations in this application's own tables refuses, leaving the
// member list and the export card intact.
func (s *Server) handleSettingsOrg(w http.ResponseWriter, r *http.Request) {
	org := identity.OrgFrom(r.Context())
	members, err := s.q.ListMembersByOrg(r.Context(), org.OrgID)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	organizationURL, navErr := s.navigator.OrganizationURL(s.cfg.AppURL + r.URL.Path)
	if navErr != nil {
		s.log.Info("identity adapter publishes no organization page",
			"env", s.cfg.Env, "path", r.URL.Path, "error", navErr)
	}
	s.Render(w, r, Page{Title: "Organization settings", Layout: templates.LayoutApp},
		templates.SettingsOrg(*org, members, organizationURL, isOrgAdmin(r)))
}
