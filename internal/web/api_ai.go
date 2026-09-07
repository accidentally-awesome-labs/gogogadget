// The route adapter for the metered chat endpoint. Same shape as the other
// API adapters: api.AI is a value of read-only handles, built per request on
// the stack, and the declared RoutePolicy.Idempotent is applied at
// registration rather than wrapped here.
package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/api"
)

func (s *Server) handleAPIAIChat(w http.ResponseWriter, r *http.Request) {
	api.AI{Q: s.q, LLM: s.llm, Catalog: s.billingCatalog, Recorder: s.usageRecorder}.Chat(w, r)
}
