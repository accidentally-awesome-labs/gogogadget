package web

import (
	"errors"
	"net/http"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/jackc/pgx/v5"
)

// POST /app/billing/checkout {plan}
func (s *Server) handleBillingCheckout(w http.ResponseWriter, r *http.Request) {
	org := identity.OrgFrom(r.Context())
	if s.billingClient == nil || s.billingCatalog == nil {
		s.renderBillingNotConfigured(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	plan := s.billingCatalog.ByKey(r.FormValue("plan"))
	if plan.ProviderProductID == "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
		s.Render(w, r, Page{Title: "Billing", Layout: templates.LayoutApp},
			templates.BillingError("That plan isn't available for checkout."))
		return
	}
	url, err := s.billingClient.CreateCheckout(r.Context(), billing.CheckoutParams{
		ProductID:          plan.ProviderProductID,
		SuccessURL:         s.cfg.AppURL + "/app/settings/billing?success=1",
		CustomerExternalID: org.OrgID,
		Metadata:           map[string]string{"org_id": org.OrgID},
	})
	if err != nil {
		s.log.Error("create checkout", "error", err)
		w.WriteHeader(http.StatusUnprocessableEntity)
		s.Render(w, r, Page{Title: "Billing", Layout: templates.LayoutApp},
			templates.BillingError("Couldn't start checkout — try again in a moment."))
		return
	}
	Redirect(w, r, url)
}

// POST /app/billing/portal
func (s *Server) handleBillingPortal(w http.ResponseWriter, r *http.Request) {
	org := identity.OrgFrom(r.Context())
	if s.billingClient == nil {
		s.renderBillingNotConfigured(w, r)
		return
	}
	url, err := s.billingClient.CreatePortalSession(r.Context(), org.OrgID)
	if err != nil {
		s.log.Error("create portal session", "error", err)
		w.WriteHeader(http.StatusUnprocessableEntity)
		s.Render(w, r, Page{Title: "Billing", Layout: templates.LayoutApp},
			templates.BillingError("Couldn't open the billing portal — try again in a moment."))
		return
	}
	Redirect(w, r, url)
}

func (s *Server) renderBillingNotConfigured(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusServiceUnavailable)
	s.Render(w, r, Page{Title: "Billing", Layout: templates.LayoutApp},
		templates.NotConfigured("Billing", "billing"))
}

// GET /app/settings/billing/fragment — polled while checkout success settles.
func (s *Server) handleSettingsBillingFragment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(r.Context())
	sub, err := s.q.GetSubscriptionByOrg(ctx, org.OrgID)
	if errors.Is(err, pgx.ErrNoRows) {
		s.Render(w, r, Page{Title: "Billing", Layout: templates.LayoutApp},
			templates.BillingCard(identity.PlanFrom(ctx), nil, true))
		return
	}
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	plan := identity.PlanFrom(ctx)
	s.Render(w, r, Page{Title: "Billing", Layout: templates.LayoutApp},
		templates.BillingCard(plan, &sub, sub.Status == "incomplete"))
}
