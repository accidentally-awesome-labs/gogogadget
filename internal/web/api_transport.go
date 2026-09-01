// API transport adapters. The generated route table names Server methods, so the
// /api/v1 surface gets thin adapters here rather than closures built inside the
// router. Composition happens once at construction — RequireAPIToken is applied
// by scope at registration, and Idempotent wraps the inner handler — so no
// request path allocates a middleware chain.
package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/api"
)

// apiSurface holds the API transport composed once at construction.
type apiSurface struct {
	middleware *api.Middleware
	// createProject and aiChat are pre-wrapped in Idempotent. The key is scoped
	// to the authenticated organization, so Idempotent must sit INSIDE
	// RequireAPIToken: identity has to be established first.
	listProjects  http.Handler
	createProject http.Handler
	aiChat        http.Handler
}

// newAPISurface composes the API transport for this server.
func newAPISurface(s *Server) apiSurface {
	middleware := api.NewMiddleware(s.q, s.cfg.APIRateLimitPerMinute)
	projects := &api.Projects{Q: s.q, Catalog: s.billingCatalog}
	ai := &api.AI{Q: s.q, LLM: s.llm, Catalog: s.billingCatalog}
	return apiSurface{
		middleware:    middleware,
		listProjects:  http.HandlerFunc(projects.ListProjects),
		createProject: middleware.Idempotent(http.HandlerFunc(projects.CreateProject)),
		aiChat:        middleware.Idempotent(http.HandlerFunc(ai.Chat)),
	}
}

// handleAPIOpenAPISpec serves the generated OpenAPI description. It is public:
// a client needs the contract before it has a token.
func (s *Server) handleAPIOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(api.OpenAPISpec)
}

func (s *Server) handleAPIListProjects(w http.ResponseWriter, r *http.Request) {
	s.api.listProjects.ServeHTTP(w, r)
}

func (s *Server) handleAPICreateProject(w http.ResponseWriter, r *http.Request) {
	s.api.createProject.ServeHTTP(w, r)
}

func (s *Server) handleAPIAIChat(w http.ResponseWriter, r *http.Request) {
	s.api.aiChat.ServeHTTP(w, r)
}
