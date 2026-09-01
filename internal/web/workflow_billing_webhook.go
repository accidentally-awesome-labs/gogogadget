package web

import (
	"errors"
	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"io"
	"net/http"
	"strings"
)

// POST /webhooks/polar. Signature parsing belongs to the selected billing
// adapter; this handler only records idempotency and dispatches neutral events.
func (s *Server) handlePolarWebhook(w http.ResponseWriter, r *http.Request) {
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	evt, err := s.billingWebhook.Verify(r.Context(), payload, r.Header)
	if err != nil {
		http.Error(w, "invalid signature", http.StatusBadRequest)
		return
	}
	if evt.Provider == "local" || evt.Provider == "dev" {
		http.Error(w, "local billing events require authenticated confirmation", http.StatusForbidden)
		return
	}
	msgID := evt.ID
	if msgID == "" {
		msgID = r.Header.Get("webhook-id")
	}
	if msgID == "" {
		http.Error(w, "missing webhook id", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if _, err = s.q.InsertWebhookEvent(ctx, sqlc.InsertWebhookEventParams{ID: msgID, Provider: evt.Provider, EventType: evt.Type}); errors.Is(err, pgx.ErrNoRows) {
		w.WriteHeader(http.StatusOK)
		return
	} else if err != nil {
		http.Error(w, "idempotency", http.StatusInternalServerError)
		return
	}
	if !strings.HasPrefix(evt.Type, "subscription.") {
		w.WriteHeader(http.StatusOK)
		return
	}
	payloadData := billing.SubscriptionPayload{ID: evt.ProviderSubscriptionID, CustomerID: evt.ProviderCustomerID, ProductID: evt.ProviderProductID, Status: evt.Status, CurrentPeriodEnd: evt.CurrentPeriodEnd, CancelAtPeriodEnd: evt.CancelAtPeriodEnd, Metadata: map[string]string{"org_id": evt.OrgIDHint}}
	processor := &billing.Processor{Q: s.q, Log: s.log, ProductPlans: s.productPlans(), Provider: evt.Provider, Emails: emailSink{s}, Capture: s.captureEvent}
	if err := processor.ProcessSubscription(ctx, evt.Type, payloadData); err != nil {
		s.log.Error("billing webhook process", "type", evt.Type, "error", err)
		http.Error(w, "processing failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
func (s *Server) productPlans() map[string]string {
	m := map[string]string{}
	for _, p := range s.billingCatalog.All() {
		if p.ProviderProductID != "" {
			m[p.ProviderProductID] = p.Key
		}
	}
	return m
}
func (s *Server) captureEvent(userID, event string, props map[string]any) {
	s.analytics.Capture(userID, event, props)
}
