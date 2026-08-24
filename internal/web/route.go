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

// registerRoutes installs every route whose Enabled predicate passes onto the
// mux its scope selects. ServeMux panics on a duplicate pattern, which is the
// property that makes migrating routes out of the hand-written table safe: a
// pattern registered in both places fails loudly instead of shadowing.
func registerRoutes(s *Server, registry []Route, targets scopeTargets) error {
	for _, route := range registry {
		if route.Enabled != nil && !route.Enabled(s) {
			continue
		}
		mux, wrap, ok := targets.target(route.Scope)
		if !ok {
			return fmt.Errorf("route %s: scope %q has no registration target", route.ID, route.Scope)
		}
		mux.Handle(route.Method+" "+route.Pattern, wrap(route.Handler(s)))
	}
	return nil
}

// routePolicies indexes the generated policy by method and pattern so the
// middleware chain can consult the declared profile instead of re-deriving one
// from the request path.
func routePolicies() map[string]RoutePolicy {
	policies := make(map[string]RoutePolicy, len(RouteRegistry))
	for _, route := range RouteRegistry {
		policies[route.Method+" "+route.Pattern] = route.Policy
	}
	return policies
}
