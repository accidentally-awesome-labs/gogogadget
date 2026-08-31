// Route records are generated from module manifests. This file owns the types
// they use; the table itself lives in routes_registry_gen.go.
package web

import (
	"fmt"
	"net/http"
)

// Scope is the closed set of middleware groups a route can belong to. It decides
// which guards wrap the handler, so it is a security boundary rather than a
// label.
type Scope string

const (
	ScopePublic   Scope = "public"
	ScopeApp      Scope = "app"
	ScopeAdmin    Scope = "admin"
	ScopeAPIRead  Scope = "api-read"
	ScopeAPIWrite Scope = "api-write"
	ScopeWebhook  Scope = "webhook"
	ScopeStatic   Scope = "static"
	ScopeProbe    Scope = "probe"
	ScopeDev      Scope = "dev"
)

// RoutePolicy is the closed transport and security profile of one route. Every
// exemption is a named field with a stated reason rather than a pattern match:
// a free-form regex over paths is how an exemption silently widens.
type RoutePolicy struct {
	// CSRFExempt drops CSRF verification. Only a request authenticated by
	// something other than a cookie may do this.
	CSRFExempt bool
	// CSRFReason states why. An exemption without one is rejected.
	CSRFReason string
	// RateExempt drops the per-IP budget.
	RateExempt bool
	// MaintenanceExempt keeps the route alive while traffic is shed.
	MaintenanceExempt bool
	// MaxBodyBytes caps the request body; 0 means the global default.
	MaxBodyBytes int64
	// Idempotent marks a mutation that may be retried with an idempotency key.
	Idempotent bool
	// AdminWrite marks a mutation that requires an administrator.
	AdminWrite bool
}

// Route is one concrete generated route. Patterns are concrete strings, so the
// generator knows every path and reserved prefix before the process boots.
type Route struct {
	ID      string
	Method  string
	Pattern string
	Scope   Scope
	Policy  RoutePolicy
	// Handler resolves the handler against the constructed server. A function
	// rather than a value because the table is package-level data and the
	// handlers are methods on a server that does not exist yet.
	Handler func(*Server) http.Handler
	// Enabled gates registration. Nil means always registered; a route that is
	// disabled is never registered at all rather than answering 404 at runtime.
	Enabled func(*Server) bool
	// ProviderActive gates routes owned by an adapter. It is evaluated with the
	// configured environment before the route enters the mux and policy index.
	ProviderActive func(*Server) bool
}

// scopeTargets names the mux that carries each scope's guards. A scope is a
// security boundary, so registration is explicit per scope rather than a single
// default mux: an /app route installed on the public mux would serve with no
// authentication at all, and nothing downstream would notice.
type scopeTargets struct {
	public *http.ServeMux
	app    *http.ServeMux
	admin  *http.ServeMux
	// apiWrap wraps an API handler in its token guard. The read/write split is
	// the declared scope, so the caller cannot forget which one a route needs.
	apiWrap func(scope string, h http.Handler) http.Handler
}

// target resolves the mux and any scope-specific wrapping for one route.
func (t scopeTargets) target(scope Scope) (*http.ServeMux, func(http.Handler) http.Handler, bool) {
	identity := func(h http.Handler) http.Handler { return h }
	switch scope {
	case ScopePublic, ScopeProbe, ScopeStatic, ScopeWebhook, ScopeDev:
		return t.public, identity, t.public != nil
	case ScopeApp:
		return t.app, identity, t.app != nil
	case ScopeAdmin:
		return t.admin, identity, t.admin != nil
	case ScopeAPIRead:
		if t.public == nil || t.apiWrap == nil {
			return nil, nil, false
		}
		return t.public, func(h http.Handler) http.Handler { return t.apiWrap("read", h) }, true
	case ScopeAPIWrite:
		if t.public == nil || t.apiWrap == nil {
			return nil, nil, false
		}
		return t.public, func(h http.Handler) http.Handler { return t.apiWrap("write", h) }, true
	}
	return nil, nil, false
}

// enabledRoutes returns the routes whose Enabled gate passes. It exists so the
// policy matcher and the mux are fed the same slice: indexing a route that was
func enabledRoutes(s *Server, registry []Route) []Route {
	enabled := make([]Route, 0, len(registry))
	for _, route := range registry {
		if route.ProviderActive != nil && !route.ProviderActive(s) { continue }
		if route.Enabled != nil && !route.Enabled(s) { continue }
		enabled = append(enabled, route)
	}
	return enabled
}

// registerRoutes installs every supplied route onto the mux its scope selects.
// ServeMux panics on a duplicate pattern, which is the property that makes
// migrating routes out of the hand-written table safe: a pattern registered in
// both places fails loudly instead of shadowing.
//
// It filters on Enabled itself as well, so a caller that skips enabledRoutes
// cannot install a gated route by accident.
func registerRoutes(s *Server, registry []Route, targets scopeTargets) error {
	for _, route := range enabledRoutes(s, registry) {
		mux, wrap, ok := targets.target(route.Scope)
		if !ok {
			return fmt.Errorf("route %s: scope %q has no registration target", route.ID, route.Scope)
		}
		mux.Handle(route.Method+" "+route.Pattern, wrap(route.Handler(s)))
	}
	return nil
}

// policyMatcher resolves a request to the policy its route declared. Matching
// runs through a real ServeMux built from the generated patterns, so wildcard
// ("/thing/{id}") and subtree ("/assets/") routes resolve by Go's own rules —
// the same rules that dispatch the request — instead of by a prefix guess. A
// prefix guess is how an exemption silently widens to cover a route nobody
// intended.
type policyMatcher struct {
	mux *http.ServeMux
}

// policyHolder carries a policy through the mux. It never serves: the matcher
// only ever asks the mux which pattern a request resolves to.
type policyHolder struct {
	policy RoutePolicy
}

func (policyHolder) ServeHTTP(http.ResponseWriter, *http.Request) {}

// newPolicyMatcher indexes the supplied routes. Patterns are already known not
// to conflict — the generator validates them against a ServeMux — so this cannot
// panic on a duplicate.
func newPolicyMatcher(routes []Route) *policyMatcher {
	mux := http.NewServeMux()
	for _, route := range routes {
		mux.Handle(route.Method+" "+route.Pattern, policyHolder{policy: route.Policy})
	}
	return &policyMatcher{mux: mux}
}

// policyFor returns the declared policy for a request, and whether any route
// claimed it. It fails closed: an undeclared path gets the zero policy, so a
// route someone forgets to declare keeps every protection rather than silently
// losing CSRF.
func (m *policyMatcher) policyFor(r *http.Request) (RoutePolicy, bool) {
	if m == nil || m.mux == nil {
		return RoutePolicy{}, false
	}
	handler, pattern := m.mux.Handler(r)
	if pattern == "" {
		return RoutePolicy{}, false
	}
	holder, ok := handler.(policyHolder)
	if !ok {
		return RoutePolicy{}, false
	}
	return holder.policy, true
}
