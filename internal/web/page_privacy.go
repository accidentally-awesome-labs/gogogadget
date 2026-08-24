package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/web/templates"
)

func (s *Server) handlePrivacy(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, Page{Title: "Privacy Policy", Layout: templates.LayoutPublic}, templates.LegalPrivacy())
}
