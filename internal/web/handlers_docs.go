package web

import (
	"net/http"
	"strings"

	"github.com/gogogadget/gogogadget/internal/i18n"

	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /docs → 303 to the lowest-weight page.
func (s *Server) handleDocsIndex(w http.ResponseWriter, r *http.Request) {
	if len(s.docs.Pages) == 0 {
		s.handleNotFound(w, r)
		return
	}
	target := "/docs/" + s.docs.Pages[0].Slug
	Navigate(w, r, target)
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
	}
	s.Render(w, r, p, templates.DocsPage(s.docs, slug))
}

// GET /docs/search?q= — ranked search over the embedded docs (AND terms).
func (s *Server) handleDocsSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	p := Page{
		Title:  i18n.T(r.Context(), "docs.search_title") + " — Docs",
		Layout: templates.LayoutDocs,
		Docs:   s.docs,
	}
	s.Render(w, r, p, templates.DocsSearchPage(query, s.docs.Search(query)))
}
