package web

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/modkit"
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

// A route whose Enabled gate refused registration must not resolve to a policy.
// In production /metrics and the five /debug/pprof/* patterns are unregistered,
// and they declare RateExempt and MaintenanceExempt. Indexing them anyway left
// those paths unmetered and answering 404 instead of 503 during maintenance,
// which is the exact inverse of what the exemption is for.
func TestEnabledRoutesGatesBothRegistrationAndPolicy(t *testing.T) {
	registry := []Route{
		{
			ID: "always", Method: http.MethodGet, Pattern: "/always", Scope: ScopePublic,
			Handler: func(*Server) http.Handler { return http.NotFoundHandler() },
		},
		{
			ID: "gated", Method: http.MethodGet, Pattern: "/metrics", Scope: ScopePublic,
			Policy:  RoutePolicy{RateExempt: true, MaintenanceExempt: true},
			Handler: func(*Server) http.Handler { return http.NotFoundHandler() },
			Enabled: func(*Server) bool { return false },
		},
	}

	enabled := enabledRoutes(nil, registry)
	if len(enabled) != 1 || enabled[0].ID != "always" {
		t.Fatalf("enabledRoutes = %d route(s), want only the ungated one", len(enabled))
	}

	matcher := newPolicyMatcher(enabled)
	request := httptest.NewRequest(http.MethodGet, "http://example.test/metrics", nil)
	policy, declared := matcher.policyFor(request)
	if declared {
		t.Fatalf("unregistered route resolved to a policy: %#v", policy)
	}
	if policy.RateExempt || policy.MaintenanceExempt {
		t.Fatalf("unregistered route yielded exemptions: %#v", policy)
	}

	// And the mux agrees: the gated pattern was never installed.
	public := http.NewServeMux()
	if err := registerRoutes(nil, registry, scopeTargets{public: public}); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}
	if _, pattern := public.Handler(request); pattern != "" {
		t.Fatalf("gated route registered as %q", pattern)
	}
}

// The same property against the real wiring, which is where the defect actually
// lived: routes() filtered on Enabled when registering and then handed the
// unfiltered slice to newPolicyMatcher. In production that made /metrics and the
// five /debug/pprof/* patterns unmetered and maintenance-exempt while being
// unreachable — a 404 instead of a 503 during maintenance, on a path with no
// rate limit.
func TestProductionGatedRoutesResolveToNoPolicy(t *testing.T) {
	server := testServer(t, func(c *config.Config) {
		c.Env = "production"
		c.MetricsToken = "" // leaves /metrics unregistered
	})
	for _, path := range []string{
		"/metrics",
		"/debug/pprof/",
		"/debug/pprof/cmdline",
		"/debug/pprof/profile",
		"/debug/pprof/symbol",
		"/debug/pprof/trace",
	} {
		request := httptest.NewRequest(http.MethodGet, "http://example.test"+path, nil)
		policy, declared := server.policies.policyFor(request)
		if declared {
			t.Errorf("%s resolved to a policy while unregistered: %#v", path, policy)
		}
		if policy.RateExempt || policy.MaintenanceExempt {
			t.Errorf("%s kept its exemptions while unregistered: %#v", path, policy)
		}
	}

	// Outside production the same routes are registered, so they must resolve.
	dev := testServer(t, nil)
	request := httptest.NewRequest(http.MethodGet, "http://example.test/metrics", nil)
	if _, declared := dev.policies.policyFor(request); !declared {
		t.Fatal("/metrics has no policy outside production, where it is registered")
	}
}

// RoutePolicy.MaxBodyBytes is generated and validated for six routes. Until it
// was read, the only cap was the global 10 MB, so /webhooks/clerk and
// /webhooks/polar declared 1 MiB and still let io.ReadAll buffer 10 MB before
// signature verification had a chance to reject it.
func TestRouteBodyLimitEnforcesDeclaredCap(t *testing.T) {
	const limit = 1024
	server := &Server{policies: newPolicyMatcher([]Route{
		{
			ID: "capped", Method: http.MethodPost, Pattern: "/capped", Scope: ScopeWebhook,
			Policy:  RoutePolicy{MaxBodyBytes: limit},
			Handler: func(*Server) http.Handler { return http.NotFoundHandler() },
		},
		{
			ID: "uncapped", Method: http.MethodPost, Pattern: "/uncapped", Scope: ScopeWebhook,
			Handler: func(*Server) http.Handler { return http.NotFoundHandler() },
		},
	})}

	var read int
	var readErr error
	handler := server.routeBodyLimit(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		read, readErr = len(body), err
	}))

	cases := []struct {
		name    string
		path    string
		size    int
		wantErr bool
	}{
		{"under the declared cap", "/capped", limit - 1, false},
		{"over the declared cap", "/capped", limit + 1, true},
		{"no declared cap", "/uncapped", limit * 64, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			read, readErr = 0, nil
			request := httptest.NewRequest(http.MethodPost, "http://example.test"+tc.path,
				bytes.NewReader(make([]byte, tc.size)))
			handler.ServeHTTP(httptest.NewRecorder(), request)
			if tc.wantErr {
				if readErr == nil {
					t.Fatalf("read %d bytes with no error; the declared cap was not applied", read)
				}
				return
			}
			if readErr != nil {
				t.Fatalf("read failed under the cap: %v", readErr)
			}
			if read != tc.size {
				t.Fatalf("read %d bytes, want %d", read, tc.size)
			}
		})
	}
}

// The global cap is declared twice: here for the runtime and in internal/modkit
// for the validator that refuses a declared MaxBodyBytes which cannot narrow it.
// The runtime must not import the CLI package that generates it, so the two are
// held together here instead.
func TestGlobalBodyLimitMatchesTheValidator(t *testing.T) {
	if globalMaxBodyBytes != modkit.GlobalRequestBodyLimit {
		t.Fatalf("global cap = %d, validator refuses at %d; a declared cap between the two would be accepted and never applied",
			globalMaxBodyBytes, modkit.GlobalRequestBodyLimit)
	}
}

// A policy value at or above the global cap must not widen anything: the global
// reader stays the binding limit.
func TestRouteBodyLimitNeverWidensTheGlobalCap(t *testing.T) {
	server := &Server{policies: newPolicyMatcher([]Route{{
		ID: "greedy", Method: http.MethodPost, Pattern: "/greedy", Scope: ScopeWebhook,
		Policy:  RoutePolicy{MaxBodyBytes: globalMaxBodyBytes * 4},
		Handler: func(*Server) http.Handler { return http.NotFoundHandler() },
	}})}

	var readErr error
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	})
	handler := maxBytes(server.routeBodyLimit(inner), globalMaxBodyBytes)

	request := httptest.NewRequest(http.MethodPost, "http://example.test/greedy",
		bytes.NewReader(make([]byte, globalMaxBodyBytes+1)))
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if readErr == nil {
		t.Fatal("a policy above the global cap widened it")
	}
}

// The webhook receivers are the reason the field exists: they io.ReadAll before
// verifying a signature. Their declared cap must survive into the live registry.
func TestWebhookReceiversDeclareATighterBodyCap(t *testing.T) {
	matcher := newPolicyMatcher(RouteRegistry)
	for _, path := range []string{"/webhooks/clerk", "/webhooks/polar"} {
		request := httptest.NewRequest(http.MethodPost, "http://example.test"+path, nil)
		policy, declared := matcher.policyFor(request)
		if !declared {
			t.Fatalf("%s is not in the generated registry", path)
		}
		if policy.MaxBodyBytes <= 0 || policy.MaxBodyBytes >= globalMaxBodyBytes {
			t.Fatalf("%s MaxBodyBytes = %d, want a cap tighter than the global %d",
				path, policy.MaxBodyBytes, globalMaxBodyBytes)
		}
	}
}
