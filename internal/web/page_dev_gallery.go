package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /dev/gallery — the component gallery. Registered only in zero-account
// mode, which config refuses under APP_ENV=production, so the kitchen sink
// cannot reach a live site. The e2e and visual-baseline runners both set
// DEV_AUTH_BYPASS, so the page is covered by axe and by a screenshot.
func (s *Server) handleDevGallery(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, Page{
		Title:  "Component gallery",
		Layout: templates.LayoutPublic,
	}, templates.Gallery())
}
