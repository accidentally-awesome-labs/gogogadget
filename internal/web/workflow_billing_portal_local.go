package web

import (
	"fmt"
	"html"
	"net/http"

	"github.com/gogogadget/gogogadget/internal/billinglocal"
	"github.com/gogogadget/gogogadget/internal/identity"
)

func (s *Server) handleBillingPortalLocal(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.billingClient.(*billinglocal.Client); !ok {
		http.Error(w, "local billing is not selected", http.StatusNotFound)
		return
	}
	org := identity.OrgFrom(r.Context())
	if org == nil {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<main><h1>Billing portal</h1><p>Local billing is managed in this application.</p><p>Organization: %s</p></main>`, html.EscapeString(org.OrgID))
}
