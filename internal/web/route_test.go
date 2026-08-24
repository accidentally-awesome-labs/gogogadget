package web

import (
	"net/http"
	"net/http/httptest"
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

// The declared policy has to be what the middleware actually consults, or
// RoutePolicy is documentation. Resolution goes through a real ServeMux built
// from the generated patterns, so wildcard and subtree routes resolve by Go's
// own matching rules rather than by a prefix guess.
func TestPolicyMatcherResolvesDeclaredPolicies(t *testing.T) {
	matcher := newPolicyMatcher([]Route{
		{ID: "exempt", Method: "POST", Pattern: "/webhooks/thing", Scope: ScopeWebhook,
			Policy:  RoutePolicy{CSRFExempt: true, CSRFReason: "signed payload", MaxBodyBytes: 4096},
			Handler: func(*Server) http.Handler { return http.NotFoundHandler() }},
		{ID: "guarded", Method: "POST", Pattern: "/app/thing", Scope: ScopeApp,
			Handler: func(*Server) http.Handler { return http.NotFoundHandler() }},
		{ID: "wildcard", Method: "GET", Pattern: "/thing/{id}", Scope: ScopePublic,
			Policy:  RoutePolicy{RateExempt: true},
			Handler: func(*Server) http.Handler { return http.NotFoundHandler() }},
		{ID: "subtree", Method: "GET", Pattern: "/assets/", Scope: ScopeStatic,
			Policy:  RoutePolicy{MaintenanceExempt: true},
			Handler: func(*Server) http.Handler { return http.NotFoundHandler() }},
	})

	cases := []struct {
		name         string
		method, path string
		found        bool
		csrf, rate   bool
		maint        bool
		maxBody      int64
	}{
		{"declared exemption", "POST", "/webhooks/thing", true, true, false, false, 4096},
		{"declared guard", "POST", "/app/thing", true, false, false, false, 0},
		{"wildcard match", "GET", "/thing/42", true, false, true, false, 0},
		{"subtree match", "GET", "/assets/app.css", true, false, false, true, 0},
		{"undeclared path", "POST", "/nowhere", false, false, false, false, 0},
		{"declared path wrong method", "GET", "/webhooks/thing", false, false, false, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, "http://example.test"+tc.path, nil)
			policy, ok := matcher.policyFor(request)
			if ok != tc.found {
				t.Fatalf("policyFor(%s %s) found = %v, want %v", tc.method, tc.path, ok, tc.found)
			}
			if policy.CSRFExempt != tc.csrf {
				t.Errorf("CSRFExempt = %v, want %v", policy.CSRFExempt, tc.csrf)
			}
			if policy.RateExempt != tc.rate {
				t.Errorf("RateExempt = %v, want %v", policy.RateExempt, tc.rate)
			}
			if policy.MaintenanceExempt != tc.maint {
				t.Errorf("MaintenanceExempt = %v, want %v", policy.MaintenanceExempt, tc.maint)
			}
			if policy.MaxBodyBytes != tc.maxBody {
				t.Errorf("MaxBodyBytes = %d, want %d", policy.MaxBodyBytes, tc.maxBody)
			}
		})
	}
}

// An undeclared path must never be exempt. Defaulting to exempt would mean any
// route someone forgets to declare silently loses CSRF protection.
func TestPolicyMatcherFailsClosed(t *testing.T) {
	matcher := newPolicyMatcher(nil)
	request := httptest.NewRequest(http.MethodPost, "http://example.test/anything", nil)
	if policy, ok := matcher.policyFor(request); ok || policy.CSRFExempt {
		t.Fatalf("empty registry yielded found=%v exempt=%v, want closed", ok, policy.CSRFExempt)
	}
}
