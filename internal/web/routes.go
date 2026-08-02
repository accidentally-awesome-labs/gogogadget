package web

import "net/http"

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

	// Auth redirects (public) → Clerk hosted Account Portal.
	s.mux.HandleFunc("GET /login", s.handleLogin)
	s.mux.HandleFunc("GET /signup", s.handleSignup)
	s.mux.HandleFunc("GET /logout", s.handleLogout)

	// Zero-account dev mode (DEV_AUTH_BYPASS is boot-refused in production).
	if s.cfg.DevAuthBypass {
		s.mux.HandleFunc("GET /dev/login", s.handleDevLogin)
		s.mux.HandleFunc("GET /dev/switch-org", s.handleDevSwitchOrg)
	}

	// Webhooks are CSRF-exempt in the chain and signature-verified here.
	s.mux.HandleFunc("POST /webhooks/clerk", s.handleClerkWebhook)

	// Guarded app group: RequireAuth → RequireNotDisabled → RequireOrg → LoadPlan.
	appMux := http.NewServeMux()
	appMux.HandleFunc("GET /app/settings/account", s.handleSettingsAccount)
	appMux.HandleFunc("GET /app/settings/org", s.handleSettingsOrg)
	appMux.HandleFunc("GET /app/activity", s.handleActivity)
	s.mux.Handle("/app", s.appChain(appMux))
	s.mux.Handle("/app/", s.appChain(appMux))

	// Catch-all 404 (least-specific pattern matches last; method-less so it
	// can't conflict with the /app and /admin subtrees).
	s.mux.HandleFunc("/{rest...}", s.handleNotFound)
}
