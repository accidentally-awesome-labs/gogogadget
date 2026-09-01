package web

import (
	"errors"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/billinglocal"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/jackc/pgx/v5"
	"github.com/justinas/nosurf"
	"html"
	"net/http"
	"time"
)

func (s *Server) handleBillingConfirm(w http.ResponseWriter, r *http.Request) {
	client, ok := s.billingClient.(*billinglocal.Client)
	if !ok {
		http.Error(w, "local billing is not selected", http.StatusNotFound)
		return
	}
	org := identity.OrgFrom(r.Context())
	if org == nil {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
	}
	product, customer, checkout := r.URL.Query().Get("product"), r.URL.Query().Get("customer"), r.URL.Query().Get("checkout")
	if product == "" {
		product = r.FormValue("product")
	}
	if customer == "" {
		customer = r.FormValue("customer")
	}
	if checkout == "" {
		checkout = r.FormValue("checkout")
	}
	if product == "" || customer == "" || customer != org.OrgID {
		http.Error(w, "invalid billing confirmation", http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<main><h1>Confirm billing</h1><form method="post" action="/app/billing/confirm"><input type="hidden" name="product" value="%s"><input type="hidden" name="customer" value="%s"><input type="hidden" name="checkout" value="%s"><input type="hidden" name="csrf_token" value="%s"><button type="submit">Confirm</button></form></main>`, html.EscapeString(product), html.EscapeString(customer), html.EscapeString(checkout), html.EscapeString(nosurf.Token(r)))
		return
	}
	eventID := "confirm:" + customer + ":" + product
	if checkout != "" {
		eventID = "confirm:" + checkout
	}
	evt := client.ConfirmedEvent(product, customer, org.OrgID)
	if err := s.processLocalBillingEvent(r, evt, eventID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/app/settings/billing?success=1", http.StatusSeeOther)
}

func (s *Server) handleBillingCancel(w http.ResponseWriter, r *http.Request) {
	client, ok := s.billingClient.(*billinglocal.Client)
	if !ok {
		http.Error(w, "local billing is not selected", http.StatusNotFound)
		return
	}
	org := identity.OrgFrom(r.Context())
	if org == nil {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
	}
	customer := r.URL.Query().Get("customer")
	if customer == "" {
		customer = r.FormValue("customer")
	}
	if customer == "" || customer != org.OrgID {
		http.Error(w, "invalid billing cancellation", http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<main><h1>Cancel billing</h1><form method="post" action="/app/billing/cancel"><input type="hidden" name="customer" value="%s"><input type="hidden" name="csrf_token" value="%s"><button type="submit">Cancel</button></form></main>`, html.EscapeString(customer), html.EscapeString(nosurf.Token(r)))
		return
	}
	sub, subErr := s.q.GetSubscriptionByOrg(r.Context(), org.OrgID)
	eventID := "cancel:" + customer
	if subErr == nil {
		product := sub.ProductKey
		eventID = "cancel:" + customer + ":" + sub.ProviderSubscriptionID.String + ":" + sub.CurrentPeriodEnd.Time.UTC().Format(time.RFC3339Nano)
		evt := client.CanceledEvent(customer, org.OrgID)
		evt.ProviderProductID = product
		evt.ProviderSubscriptionID = sub.ProviderSubscriptionID.String
		if err := s.processLocalBillingEvent(r, evt, eventID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/app/settings/billing", http.StatusSeeOther)
		return
	}
	if !errors.Is(subErr, pgx.ErrNoRows) {
		http.Error(w, subErr.Error(), http.StatusInternalServerError)
		return
	}
	http.Error(w, "no active local subscription", http.StatusNotFound)
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
