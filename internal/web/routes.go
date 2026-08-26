package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/analytics"
	"github.com/gogogadget/gogogadget/internal/api"
)

// routes installs the server's handlers. No individual route is written here:
// every one comes from RouteRegistry, generated from the selected modules'
// manifests, and is registered below. What stays is the scaffolding a generated
// record cannot describe - the scope group muxes and their middleware chains,
// the one handler whose registration depends on configuration, and the
// catch-alls.
func (s *Server) routes() error {
	// Guarded app group: RequireAuth → RequireNotDisabled → RequireOrg → LoadPlan.
	appMux := http.NewServeMux()
	s.mux.Handle("/app", s.appChain(appMux))
	s.mux.Handle("/app/", s.appChain(appMux))

	// Admin group: appChain + RequireAdmin.
	adminMux := http.NewServeMux()
	s.mux.Handle("/admin", s.adminChain(adminMux))
	s.mux.Handle("/admin/", s.adminChain(adminMux))

	// Unknown /api/ routes get the JSON 404, never the HTML one. The generated
	// API patterns are more specific, so mux precedence keeps them ahead of this
	// regardless of registration order.
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

	// Each generated record's scope picks the mux carrying that scope's guards,
	// so this runs after the group muxes exist. ServeMux panics on a duplicate
	// pattern, so a generated route that also has a hand-written registration
	// above fails loudly instead of shadowing.
	registry := RouteRegistry
	if s.testOnlyModules {
		// Opt-in only. Nothing in the production boot path sets this, so these
		// patterns cannot be reached by a deployed runtime.
		registry = append(append([]Route{}, registry...), testOnlyRoutes()...)
	}
	// The matcher is built from the same slice that gets registered, so the
	// policy the middleware consults can never drift from the route that was
	// actually installed.
	s.policies = newPolicyMatcher(registry)

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

	// Catch-all 404: the least-specific pattern, and method-less so it cannot
	// conflict with the /app and /admin subtrees or with any generated pattern.
	s.mux.HandleFunc("/{rest...}", s.handleNotFound)
	return nil
}
