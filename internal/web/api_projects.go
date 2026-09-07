// The route adapters for the projects JSON API. The generated route table
// names Server methods, so each declared route gets a thin adapter here rather
// than a closure built inside the router.
//
// api.Projects is a value built per request. It carries two read-only handles
// and no handler stores it, so it stays on the stack: there is nothing left to
// pre-compose. The idempotency middleware, which is the one thing that did need
// composing once, is applied at registration from the route's declared policy.
package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/api"
)

func (s *Server) handleAPIListProjects(w http.ResponseWriter, r *http.Request) {
	api.Projects{Q: s.q, Catalog: s.billingCatalog}.ListProjects(w, r)
}

func (s *Server) handleAPICreateProject(w http.ResponseWriter, r *http.Request) {
	api.Projects{Q: s.q, Catalog: s.billingCatalog}.CreateProject(w, r)
}
