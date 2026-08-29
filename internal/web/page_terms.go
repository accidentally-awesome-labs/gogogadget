package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/web/templates"
)

func (s *Server) handleTerms(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, Page{Title: "Terms of Service", Layout: templates.LayoutPublic}, templates.LegalTerms())
}
