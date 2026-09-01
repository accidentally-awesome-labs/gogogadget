package web

import (
	"errors"
	"net/http"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/billinglocal"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

func (s *Server) handleBillingConfirm(w http.ResponseWriter, r *http.Request) {
	client, ok := s.billingClient.(*billinglocal.Client)
	if !ok {
		http.Error(w, "local billing is not selected", http.StatusNotFound)
		return
	}
	product, customer, orgID := r.URL.Query().Get("product"), r.URL.Query().Get("customer"), r.URL.Query().Get("org")
	if product == "" || customer == "" {
		http.Error(w, "product and customer are required", http.StatusBadRequest)
		return
	}
	evt := client.ConfirmedEvent(product, customer, orgID)
	if err := s.processLocalBillingEvent(r, evt, "confirm:"+customer+":"+product); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte("<main><h1>Billing confirmed</h1><p>Your subscription is active.</p></main>"))
}

func (s *Server) handleBillingCancel(w http.ResponseWriter, r *http.Request) {
	client, ok := s.billingClient.(*billinglocal.Client)
	if !ok {
		http.Error(w, "local billing is not selected", http.StatusNotFound)
		return
	}
	customer, orgID := r.URL.Query().Get("customer"), r.URL.Query().Get("org")
	if customer == "" {
		http.Error(w, "customer is required", http.StatusBadRequest)
		return
	}
	evt := client.CanceledEvent(customer, orgID)
	if err := s.processLocalBillingEvent(r, evt, "cancel:"+customer); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte("<main><h1>Billing canceled</h1></main>"))
}

func (s *Server) processLocalBillingEvent(r *http.Request, evt billing.SubscriptionEvent, id string) error {
	ctx := r.Context()
	if _, err := s.q.InsertWebhookEvent(ctx, sqlc.InsertWebhookEventParams{ID: id, Provider: evt.Provider, EventType: evt.Type}); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	payload := billing.SubscriptionPayload{ID: evt.ProviderSubscriptionID, CustomerID: evt.ProviderCustomerID, ProductID: evt.ProviderProductID, Status: evt.Status, CurrentPeriodEnd: evt.CurrentPeriodEnd, CancelAtPeriodEnd: evt.CancelAtPeriodEnd, Metadata: map[string]string{"org_id": evt.OrgIDHint}}
	return (&billing.Processor{Q: s.q, Log: s.log, ProductPlans: s.productPlans(), Provider: evt.Provider, Emails: emailSink{s}, Capture: s.captureEvent}).ProcessSubscription(ctx, evt.Type, payload)
}
