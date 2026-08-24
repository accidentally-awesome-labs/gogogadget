package web

import (
	"net/http"
	"net/http/pprof"

	"github.com/gogogadget/gogogadget/internal/analytics"
	"github.com/gogogadget/gogogadget/internal/api"
)

func (s *Server) routes() error {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	// /metrics: bearer-gated when METRICS_TOKEN is set; unregistered in
	// production without one (internal Go stats must not be public default).
	if !s.cfg.Production() || s.cfg.MetricsToken != "" {
		s.mux.HandleFunc("GET /metrics", s.handleMetrics)
	}
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)

	s.mux.Handle("GET /static/", s.serveStatic())
	s.mux.HandleFunc("GET /favicon.ico", s.faviconRedirect)

	s.mux.HandleFunc("GET /media/{id}/{filename}", s.handleMedia)
	s.mux.HandleFunc("GET /rss.xml", s.handleRSS)
	s.mux.HandleFunc("GET /sitemap.xml", s.handleSitemap)
	s.mux.HandleFunc("GET /robots.txt", s.handleRobots)

	// Docs (in-app, versioned with the code).
	s.mux.HandleFunc("GET /docs", s.handleDocsIndex)
	s.mux.HandleFunc("GET /docs/{slug}", s.handleDocsPage)
	// Exact literal beats the {slug} wildcard per mux precedence rules.
	s.mux.HandleFunc("GET /docs/search", s.handleDocsSearch)

	// Auth redirects (public) → Clerk hosted Account Portal.

	// Zero-account dev mode (DEV_AUTH_BYPASS is boot-refused in production).
	if s.cfg.DevAuthBypass {
	}

	// Locale switch (public; NOT CSRF-exempt — switcher forms carry the token
	// via the body-inherited X-CSRF-Token header).
	s.mux.HandleFunc("POST /set-locale", s.handleSetLocale)

	// Webhooks are CSRF-exempt in the chain and signature-verified here.
	s.mux.HandleFunc("POST /webhooks/clerk", s.handleClerkWebhook)

	// Guarded app group: RequireAuth → RequireNotDisabled → RequireOrg → LoadPlan.
	appMux := http.NewServeMux()

	appMux.HandleFunc("GET /app/settings/webhooks", s.handleSettingsWebhooks)
	appMux.HandleFunc("POST /app/settings/webhooks/endpoints", s.handleWebhookEndpointCreate)
	appMux.HandleFunc("POST /app/settings/webhooks/endpoints/{id}/disable", s.handleWebhookEndpointToggle)
	appMux.HandleFunc("POST /app/settings/webhooks/endpoints/{id}/enable", s.handleWebhookEndpointToggle)
	appMux.HandleFunc("POST /app/settings/webhooks/endpoints/{id}/rotate", s.handleWebhookEndpointRotate)
	appMux.HandleFunc("POST /app/settings/webhooks/deliveries/{id}/replay", s.handleWebhookDeliveryReplay)
	s.mux.Handle("/app", s.appChain(appMux))
	s.mux.Handle("/app/", s.appChain(appMux))

	// Admin group: appChain + RequireAdmin.
	adminMux := http.NewServeMux()
	adminMux.HandleFunc("GET /admin/jobs", s.handleAdminJobs)
	adminMux.HandleFunc("POST /admin/jobs/{id}/requeue", s.handleAdminJobRequeue)
	// Content CMS: kind is a QUERY parameter resolved through the registry,
	// never a path segment — that is what lets a newly registered type arrive
	// with zero routing changes. Reads are GET and mutations POST, so
	// requireAdminWrite gates support by method with no per-route wiring.
	s.mux.Handle("/admin", s.adminChain(adminMux))
	s.mux.Handle("/admin/", s.adminChain(adminMux))

	// Public JSON API: cookieless Bearer auth (CSRF-exempt in the chain).
	// The contract itself: unauthenticated so tooling can read it before a
	// token exists. Registered before the /api/ catch-all 404.
	s.mux.HandleFunc("GET /api/v1/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(api.OpenAPISpec)
	})

	apiMW := api.NewMiddleware(s.q, s.cfg.APIRateLimitPerMinute)
	apiProjects := &api.Projects{Q: s.q}
	s.mux.Handle("GET /api/v1/projects", apiMW.RequireAPIToken("read", http.HandlerFunc(apiProjects.ListProjects)))
	// Unsafe verbs run inside Idempotent, which sits under RequireAPIToken:
	// the key is scoped to the authenticated org, so it needs identity first.
	s.mux.Handle("POST /api/v1/projects", apiMW.RequireAPIToken("write",
		apiMW.Idempotent(http.HandlerFunc(apiProjects.CreateProject))))
	apiAI := &api.AI{Q: s.q, LLM: s.llm}
	s.mux.Handle("POST /api/v1/ai/chat", apiMW.RequireAPIToken("write",
		apiMW.Idempotent(http.HandlerFunc(apiAI.Chat))))
	// Unknown /api/ routes get the JSON 404, never the HTML one.
	s.mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.WriteError(w, http.StatusNotFound, "not_found", "Unknown API route.")
	}))

	// PostHog reverse proxy (registered only when configured; CSRF- and
	// rate-limit-exempt in the chain).
	if s.cfg.PostHogEnabled() {
		if proxy, err := analytics.IngestProxy(s.cfg.PostHogHost); err == nil {
			s.mux.Handle("/ingest/", proxy)
		} else {
			s.log.Error("posthog proxy", "error", err)
		}
	}

	// pprof outside production only (zero-config profiling).
	if !s.cfg.Production() {
		s.mux.HandleFunc("GET /debug/pprof/", pprof.Index)
		s.mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
		s.mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
		s.mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
		s.mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	}

	// Catch-all 404 (least-specific pattern matches last; method-less so it
	// can't conflict with the /app and /admin subtrees).
	// Generated routes come from module manifests, and each record's scope picks
	// the mux carrying that scope's guards. This runs after the group muxes exist
	// and before the catch-all, so a generated pattern still wins over it.
	// ServeMux panics on a duplicate pattern, so a route that also has a
	// hand-written registration above fails loudly instead of shadowing.
	registry := RouteRegistry
	if s.testOnlyModules {
		// Opt-in only. Nothing in the production boot path sets this, so these
		// patterns cannot be reached by a deployed runtime.
		registry = append(append([]Route{}, registry...), testOnlyRoutes()...)
	}
	if err := registerRoutes(s, registry, scopeTargets{
		public: s.mux,
		app:    appMux,
		admin:  adminMux,
		apiWrap: func(scope string, h http.Handler) http.Handler {
			return apiMW.RequireAPIToken(scope, h)
		},
	}); err != nil {
		return err
	}

	s.mux.HandleFunc("/{rest...}", s.handleNotFound)
	return nil
}
