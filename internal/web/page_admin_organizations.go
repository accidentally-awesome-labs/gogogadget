package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /admin/orgs — member counts + plan badges.
func (s *Server) handleAdminOrgs(w http.ResponseWriter, r *http.Request) {
	orgs, err := s.q.ListOrgsWithStats(r.Context(), sqlc.ListOrgsWithStatsParams{Limit: 100, Offset: 0})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.Render(w, r, Page{Title: "Organizations", Layout: templates.LayoutAdmin},
		templates.AdminOrgsPage(templates.AdminOrgsData{Orgs: orgs}))
}
