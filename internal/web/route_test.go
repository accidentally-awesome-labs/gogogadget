package web

import (
	"net/http"
	"testing"
)

// Scope decides which guards wrap a route, so registration must send each record
// to the mux that carries those guards. Registering an /app route on the public
// mux would serve it with no authentication at all — the failure is silent and
// total, so it gets a direct test.
func TestRegisterGeneratedDispatchesByScope(t *testing.T) {
	public := http.NewServeMux()
	app := http.NewServeMux()
	admin := http.NewServeMux()

	registry := []Route{
		{ID: "p", Method: "GET", Pattern: "/public-probe", Scope: ScopePublic,
			Handler: func(*Server) http.Handler { return http.NotFoundHandler() }},
		{ID: "a", Method: "GET", Pattern: "/app/thing", Scope: ScopeApp,
			Handler: func(*Server) http.Handler { return http.NotFoundHandler() }},
		{ID: "d", Method: "GET", Pattern: "/admin/thing", Scope: ScopeAdmin,
			Handler: func(*Server) http.Handler { return http.NotFoundHandler() }},
	}

	targets := scopeTargets{public: public, app: app, admin: admin}
	if err := registerRoutes(nil, registry, targets); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}

	for name, probe := range map[string]struct {
		mux  *http.ServeMux
		path string
		want bool
	}{
		"public route on public mux": {public, "/public-probe", true},
		"app route not on public":    {public, "/app/thing", false},
		"app route on app mux":       {app, "/app/thing", true},
		"admin route on admin mux":   {admin, "/admin/thing", true},
		"admin route not on app":     {app, "/admin/thing", false},
	} {
		t.Run(name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodGet, "http://example.test"+probe.path, nil)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			_, pattern := probe.mux.Handler(request)
			if got := pattern != ""; got != probe.want {
				t.Fatalf("pattern for %s = %q, registered=%v want %v", probe.path, pattern, got, probe.want)
			}
		})
	}
}

// A scope with no target is a wiring bug: silently dropping the route would
// remove a surface with no failure anywhere.
func TestRegisterRoutesRejectsUnroutableScope(t *testing.T) {
	registry := []Route{
		{ID: "a", Method: "GET", Pattern: "/app/thing", Scope: ScopeApp,
			Handler: func(*Server) http.Handler { return http.NotFoundHandler() }},
	}
	err := registerRoutes(nil, registry, scopeTargets{public: http.NewServeMux()})
	if err == nil {
		t.Fatal("registerRoutes accepted a route whose scope has no target mux")
	}
}
