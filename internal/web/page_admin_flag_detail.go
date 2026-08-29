package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /admin/flags/{key} — flag detail with per-org overrides.
func (s *Server) handleAdminFlagDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key := r.PathValue("key")
	flag, err := s.q.GetFeatureFlag(ctx, key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	overrides, err := s.q.ListFlagOverridesByFlag(ctx, key)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	orgs, err := s.q.ListOrgs(ctx)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.Render(w, r, Page{Title: flag.Key + " — " + i18n.T(ctx, "flags.title"), Layout: templates.LayoutAdmin},
		templates.AdminFlagDetailPage(flag, overrides, orgs))
}
