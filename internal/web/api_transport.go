// The /api/v1 transport core. What lives here is what the server itself needs
// to register any API route at all: the token middleware, composed once at
// construction. Each resource's adapter lives with that resource — the shell
// cannot name a product resource's transport without depending on the product.
package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/api"
)

// apiSurface holds the API transport composed once at construction.
type apiSurface struct {
	middleware *api.Middleware
}

// newAPISurface composes the API transport for this server.
func newAPISurface(s *Server) apiSurface {
	return apiSurface{middleware: api.NewMiddleware(s.q, s.cfg.APIRateLimitPerMinute)}
}

// apiIdempotent wraps a handler in the idempotency-key middleware. It is
// applied at REGISTRATION, from the route's declared RoutePolicy.Idempotent,
// and inside RequireAPIToken: the key is scoped to the authenticated
// organization, so identity has to be established first.
func (s *Server) apiIdempotent(h http.Handler) http.Handler {
	return s.api.middleware.Idempotent(h)
}
