package web

import (
	"fmt"
	"net/http"

	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

func (s *Server) handleBlogIndex(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, Page{
		Title:       "Blog",
		Description: "Product and engineering updates from the GoGoGadget team.",
		Layout:      templates.LayoutPublic,
	}, templates.BlogIndex(s.blog.Posts))
}

func (s *Server) handleBlogPost(w http.ResponseWriter, r *http.Request) {
	post := s.blog.BySlug(r.PathValue("slug"))
	if post == nil {
		s.handleNotFound(w, r)
		return
	}
	s.Render(w, r, Page{
		Title:       post.Title,
		Description: post.Description,
		Layout:      templates.LayoutPublic,
		JSONLD:      s.postJSONLD(*post),
	}, templates.BlogPost(*post))
}

func (s *Server) handleRSS(w http.ResponseWriter, r *http.Request) {
	feed, err := content.RSS(s.cfg.AppURL, s.blog.Posts)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	fmt.Fprint(w, feed)
}

func (s *Server) handleRobots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "User-agent: *\nAllow: /\nSitemap: %s/sitemap.xml\n", s.cfg.AppURL)
}

// handleSitemap lists every indexable URL. lastmod is emitted ONLY for blog
// posts, the one collection carrying a real date in its frontmatter: a
// fabricated timestamp on the marketing and docs pages (build time, "today")
// teaches crawlers to distrust the field, which is worse than omitting it.
func (s *Server) handleSitemap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>`+"\n")
	fmt.Fprint(w, `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	for _, u := range []string{"/", "/pricing", "/terms", "/privacy", "/blog", "/docs"} {
		fmt.Fprintf(w, "<url><loc>%s%s</loc></url>", s.cfg.AppURL, u)
	}
	for _, p := range s.blog.Posts {
		fmt.Fprintf(w, "<url><loc>%s/blog/%s</loc><lastmod>%s</lastmod></url>",
			s.cfg.AppURL, p.Slug, p.Date.UTC().Format("2006-01-02"))
	}
	for _, d := range s.docs.Pages {
		fmt.Fprintf(w, "<url><loc>%s/docs/%s</loc></url>", s.cfg.AppURL, d.Slug)
	}
	fmt.Fprint(w, `</urlset>`)
}
