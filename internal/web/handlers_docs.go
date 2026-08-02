package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /docs → 303 to the lowest-weight page.
func (s *Server) handleDocsIndex(w http.ResponseWriter, r *http.Request) {
	if len(s.docs.Pages) == 0 {
		s.handleNotFound(w, r)
		return
	}
	target := "/docs/" + s.docs.Pages[0].Slug
	Redirect(w, r, target)
}

// GET /docs/{slug}
func (s *Server) handleDocsPage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	page := s.docs.BySlug(slug)
	if page == nil {
		s.handleNotFound(w, r)
		return
	}
	p := Page{
		Title:       page.Title + " — Docs",
		Description: page.Description,
		Layout:      templates.LayoutDocs,
		Docs:        s.docs,
		DocSlug:     slug,
	}
	s.Render(w, r, p, templates.DocsPage(s.docs, slug))
}
