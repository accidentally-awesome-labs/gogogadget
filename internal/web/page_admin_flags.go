package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/flags"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /admin/flags — feature-flag table (admin only via adminChain).
func (s *Server) handleAdminFlags(w http.ResponseWriter, r *http.Request) {
	provided, err := s.flags.List(r.Context())
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	flags := flagRows(provided)
	s.Render(w, r, Page{Title: i18n.T(r.Context(), "flags.title"), Layout: templates.LayoutAdmin},
		templates.AdminFlagsPage(templates.FlagsData{Flags: flags}))
}

func flagRows(in []flags.Flag) []sqlc.FeatureFlag {
	out := make([]sqlc.FeatureFlag, 0, len(in))
	for _, f := range in {
		out = append(out, sqlc.FeatureFlag{Key: f.Key, Description: f.Description, Enabled: f.Enabled, Rollout: int32(f.Rollout)})
	}
	return out
}
