package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

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
	wh, err := standardwebhooks.NewWebhookRaw([]byte(s.cfg.PolarWebhookSecret))
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
