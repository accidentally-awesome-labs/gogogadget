package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

func (s *Server) handlePricing(w http.ResponseWriter, r *http.Request) {
	authed := identity.UserFrom(r.Context()) != nil && identity.OrgFrom(r.Context()) != nil
	currentPlan := ""
	if authed {
		currentPlan = billing.CurrentPlan(r.Context(), s.q, identity.OrgFrom(r.Context()).ClerkOrgID, s.cfg.Now()).Key
	}
	s.Render(w, r, Page{
		Title:       "Pricing",
		Description: "Simple pricing that scales with you. Start free, upgrade when you outgrow it.",
		Layout:      templates.LayoutPublic,
	}, templates.Pricing(billing.Plans, authed, currentPlan))
}
