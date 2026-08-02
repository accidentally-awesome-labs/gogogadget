package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/jackc/pgx/v5"
	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

// POST /app/billing/checkout {plan}
func (s *Server) handleBillingCheckout(w http.ResponseWriter, r *http.Request) {
	org := identity.OrgFrom(r.Context())
	if !s.cfg.PolarConfigured() || s.billingClient == nil {
		s.renderBillingNotConfigured(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	plan := billing.PlanByKey(r.FormValue("plan"))
	if plan.PolarProductID == "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
		s.Render(w, r, Page{Title: "Billing", Layout: templates.LayoutApp},
			templates.BillingError("That plan isn't available for checkout."))
		return
	}
	url, err := s.billingClient.CreateCheckout(r.Context(), billing.CheckoutParams{
		ProductID:          plan.PolarProductID,
		SuccessURL:         s.cfg.AppURL + "/app/settings/billing?success=1",
		CustomerExternalID: org.ClerkOrgID,
		Metadata:           map[string]string{"clerk_org_id": org.ClerkOrgID},
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
	if !s.cfg.PolarConfigured() || s.billingClient == nil {
		s.renderBillingNotConfigured(w, r)
		return
	}
	url, err := s.billingClient.CreatePortalSession(r.Context(), org.ClerkOrgID)
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

// GET /app/settings/billing
func (s *Server) handleSettingsBilling(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(r.Context())
	plan := identity.PlanFrom(ctx)
	sub := identity.SubFrom(ctx)

	// Success race: redirected back from checkout before the webhook landed →
	// render a polling fragment instead of asserting on immediate state.
	processing := r.URL.Query().Get("success") == "1" && (sub == nil || sub.Status == "incomplete")

	count, err := s.q.CountProjectsByOrg(ctx, org.ClerkOrgID)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.Render(w, r, Page{Title: "Billing", Layout: templates.LayoutApp}, templates.SettingsBilling(templates.BillingData{
		Plan: plan, Sub: sub, ProjectCount: count,
		Plans: billing.Plans, Processing: processing,
		Success: r.URL.Query().Get("success") == "1",
	}))
}

// GET /app/settings/billing/fragment — polled while checkout success settles.
func (s *Server) handleSettingsBillingFragment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(r.Context())
	sub, err := s.q.GetSubscriptionByOrg(ctx, org.ClerkOrgID)
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

// POST /webhooks/polar — Polar delivers with webhook-* headers (Standard
// Webhooks), verified with POLAR_WEBHOOK_SECRET.
func (s *Server) handlePolarWebhook(w http.ResponseWriter, r *http.Request) {
	if s.cfg.PolarWebhookSecret == "" {
		http.Error(w, "polar webhooks not configured", http.StatusServiceUnavailable)
		return
	}
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	wh, err := standardwebhooks.NewWebhook(s.cfg.PolarWebhookSecret)
	if err != nil {
		s.log.Error("polar webhook init", "error", err)
		http.Error(w, "webhook config", http.StatusInternalServerError)
		return
	}
	if err := wh.Verify(payload, r.Header); err != nil {
		http.Error(w, "invalid signature", http.StatusBadRequest)
		return
	}

	msgID := r.Header.Get("webhook-id")
	if msgID == "" {
		http.Error(w, "missing webhook-id", http.StatusBadRequest)
		return
	}
	var evt struct {
		Type string                      `json:"type"`
		Data billing.SubscriptionPayload `json:"data"`
	}
	if err := json.Unmarshal(payload, &evt); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	_, err = s.q.InsertWebhookEvent(ctx, sqlc.InsertWebhookEventParams{ID: msgID, Provider: "polar", EventType: evt.Type})
	if errors.Is(err, pgx.ErrNoRows) {
		w.WriteHeader(http.StatusOK) // replay → already processed
		return
	}
	if err != nil {
		http.Error(w, "idempotency", http.StatusInternalServerError)
		return
	}

	processor := &billing.Processor{
		Q: s.q, Log: s.log,
		ProductPlans: s.productPlans(),
		Emails:       emailSink{s},
		Capture:      s.captureEvent,
	}
	if !strings.HasPrefix(evt.Type, "subscription.") {
		s.log.Info("polar webhook: non-subscription event (ignored)", "type", evt.Type)
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := processor.ProcessSubscription(ctx, evt.Type, evt.Data); err != nil {
		s.log.Error("polar webhook process", "type", evt.Type, "error", err)
		http.Error(w, "processing failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// productPlans reverse-maps Polar product IDs to plan keys.
func (s *Server) productPlans() map[string]string {
	m := map[string]string{}
	for _, p := range billing.Plans {
		if p.PolarProductID != "" {
			m[p.PolarProductID] = p.Key
		}
	}
	return m
}

// captureEvent is the analytics hook for the billing processor; the
// NoopCapturer makes it a no-op until PostHog is configured.
func (s *Server) captureEvent(userID, event string, props map[string]any) {
	s.analytics.Capture(userID, event, props)
}
