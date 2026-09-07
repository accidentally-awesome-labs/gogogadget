package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/api"
)

// handleAPIOpenAPISpec serves the generated OpenAPI description. It is public:
// a client needs the contract before it has a token.
func (s *Server) handleAPIOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(api.OpenAPISpec)
}
