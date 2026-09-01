package search

import (
	"encoding/json"
	"net/http"
)

// ServeQuery is the transport boundary for search consumers. Provider failures
// are dependency failures, not empty results: callers receive a retryable 503.
func ServeQuery(w http.ResponseWriter, r *http.Request, index Index, query Query) {
	if index == nil {
		http.Error(w, "search_unavailable", http.StatusServiceUnavailable)
		return
	}
	result, err := index.Query(r.Context(), query)
	if err != nil {
		http.Error(w, "search_unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
