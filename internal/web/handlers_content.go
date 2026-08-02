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

func (s *Server) handleSitemap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>`+"\n")
	fmt.Fprint(w, `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	urls := []string{"/", "/pricing", "/terms", "/privacy", "/blog"}
	for _, p := range s.blog.Posts {
		urls = append(urls, "/blog/"+p.Slug)
	}
	for _, u := range urls {
		fmt.Fprintf(w, "<url><loc>%s%s</loc></url>", s.cfg.AppURL, u)
	}
	fmt.Fprint(w, `</urlset>`)
}
