package web

import (
	"net/http"

	"github.com/a-h/templ"
	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /admin/content/{id}/preview — the entry rendered through its OWN public
// view, regardless of status or dates. Same contentViews lookup as the public
// route, so what an editor previews is what readers get.
func (s *Server) handleAdminContentPreviewPage(w http.ResponseWriter, r *http.Request) {
	entry, t, ok := s.contentEntry(w, r)
	if !ok {
		return
	}
	if t.Path == "" {
		s.handleNotFound(w, r) // no public rendering exists for this type
		return
	}
	e := content.EntryFrom(entry)
	view := s.contentViews()[t.Kind]
	page := Page{Title: e.Title, Description: e.Summary, Layout: templates.LayoutPublic}
	var component templ.Component
	switch {
	case t.Mode == content.ModeSinglePage:
		// A single-page type has no per-entry URL: preview it as a
		// one-element collection.
		if view.page != nil {
			page = view.page(t, nil)
		}
		component = s.contentIndexComponent(t, view, []content.Entry{e})
	case view.detail != nil:
		if view.page != nil {
			page = view.page(t, &e)
		}
		component = view.detail(t, e)
	default:
		component = templates.ContentDetail(t, e)
	}
	s.Render(w, r, page, component)
}
