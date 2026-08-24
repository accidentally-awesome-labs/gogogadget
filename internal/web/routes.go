package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/analytics"
	"github.com/gogogadget/gogogadget/internal/api"
)

func (s *Server) routes() error {

	// Docs (in-app, versioned with the code).
	// Exact literal beats the {slug} wildcard per mux precedence rules.

	// Auth redirects (public) → Clerk hosted Account Portal.

	// Zero-account dev mode (DEV_AUTH_BYPASS is boot-refused in production).
	if s.cfg.DevAuthBypass {
	}

	// Locale switch (public; NOT CSRF-exempt — switcher forms carry the token
	// via the body-inherited X-CSRF-Token header).

	// Webhooks are CSRF-exempt in the chain and signature-verified here.

	// Guarded app group: RequireAuth → RequireNotDisabled → RequireOrg → LoadPlan.
	appMux := http.NewServeMux()

	s.mux.Handle("/app", s.appChain(appMux))
	s.mux.Handle("/app/", s.appChain(appMux))

	// Admin group: appChain + RequireAdmin.
	adminMux := http.NewServeMux()
	// Content CMS: kind is a QUERY parameter resolved through the registry,
	// never a path segment — that is what lets a newly registered type arrive
	// with zero routing changes. Reads are GET and mutations POST, so
	// requireAdminWrite gates support by method with no per-route wiring.
	s.mux.Handle("/admin", s.adminChain(adminMux))
	s.mux.Handle("/admin/", s.adminChain(adminMux))

	// Public JSON API: cookieless Bearer auth (CSRF-exempt in the chain).
	// The contract itself: unauthenticated so tooling can read it before a
	// token exists. Registered before the /api/ catch-all 404.
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
			return s.api.middleware.RequireAPIToken(scope, h)
		},
	}); err != nil {
		return err
	}

	s.mux.HandleFunc("/{rest...}", s.handleNotFound)
	return nil
}
