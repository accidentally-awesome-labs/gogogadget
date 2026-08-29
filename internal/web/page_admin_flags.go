package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /admin/flags — feature-flag table (admin only via adminChain).
func (s *Server) handleAdminFlags(w http.ResponseWriter, r *http.Request) {
	flags, err := s.q.ListFeatureFlags(r.Context())
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.Render(w, r, Page{Title: i18n.T(r.Context(), "flags.title"), Layout: templates.LayoutAdmin},
		templates.AdminFlagsPage(templates.FlagsData{Flags: flags}))
}
