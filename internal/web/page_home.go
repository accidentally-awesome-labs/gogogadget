package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, Page{
		Title:       "Ship your SaaS this weekend",
		Description: "The production-grade Go + HTMX SaaS boilerplate: auth, teams, billing, email, admin, blog, and docs out of the box.",
		Layout:      templates.LayoutPublic,
		JSONLD:      s.siteJSONLD(),
	}, templates.Home(billing.Plans))
}
