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
		currentPlan = billing.CurrentPlanWithCatalog(r.Context(), s.q, identity.OrgFrom(r.Context()).OrgID, s.cfg.Now(), s.billingCatalog).Key
	}
	s.Render(w, r, Page{
		Title:       "Pricing",
		Description: "Simple plans that scale with your work.",
		Layout:      templates.LayoutPublic,
	}, templates.Pricing(s.billingCatalog.All(), authed, currentPlan))
}
