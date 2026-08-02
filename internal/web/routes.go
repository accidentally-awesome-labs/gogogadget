package web

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)

	s.mux.Handle("GET /static/", s.serveStatic())
	s.mux.HandleFunc("GET /favicon.ico", s.faviconRedirect)

	// Public marketing pages.
	s.mux.HandleFunc("GET /{$}", s.handleHome)
	s.mux.HandleFunc("GET /pricing", s.handlePricing)
	s.mux.HandleFunc("GET /terms", s.handleTerms)
	s.mux.HandleFunc("GET /privacy", s.handlePrivacy)

	// Content: blog, feed, SEO.
	s.mux.HandleFunc("GET /blog", s.handleBlogIndex)
	s.mux.HandleFunc("GET /blog/{slug}", s.handleBlogPost)
	s.mux.HandleFunc("GET /rss.xml", s.handleRSS)
	s.mux.HandleFunc("GET /sitemap.xml", s.handleSitemap)
	s.mux.HandleFunc("GET /robots.txt", s.handleRobots)

	// Catch-all 404 (least-specific pattern matches last).
	s.mux.HandleFunc("GET /{rest...}", s.handleNotFound)
}
