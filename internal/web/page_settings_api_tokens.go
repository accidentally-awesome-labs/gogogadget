package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /app/settings/api — token management. Any org member may create/revoke
// org tokens (boilerplate default; tightening to org:admin is one middleware
// line — see README).
func (s *Server) handleSettingsAPI(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.q.ListAPITokensByOrg(r.Context(), identity.OrgFrom(r.Context()).OrgID)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.Render(w, r, Page{Title: "API tokens", Layout: templates.LayoutApp},
		templates.SettingsAPI(templates.APITokensData{Tokens: tokens}))
}
