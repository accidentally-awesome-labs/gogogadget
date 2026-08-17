// Package webhooks is the outbound-webhook seam: customer endpoints subscribe
// to org events; deliveries ride the Postgres job queue with the existing
// backoff/dead-letter machinery. Signing uses the standard-webhooks format
// (webhook-id/-timestamp/-signature) — the same lib that verifies INBOUND
// Polar webhooks.
package webhooks

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/jobs"
)

// EventTypes is the catalog the settings UI renders (checkboxes) and Emit
// accepts. '{}' subscription receives everything.
var EventTypes = []string{
	"project.created",
	"project.updated",
	"project.archived",
	"project.deleted",
}

// Envelope is the payload shape every delivery carries.
type Envelope struct {
	Type       string         `json:"type"`
	OccurredAt string         `json:"occurred_at"`
	Data       map[string]any `json:"data"`
}

// NewSecret generates an endpoint signing secret ("whsec_" + 43B base64url).
// Stored (needed for signing); shown to the customer exactly once.
func NewSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error()) // never happens on supported platforms
	}
	return "whsec_" + base64.URLEncoding.EncodeToString(b)
}

// Emit fans one event out to every matching active endpoint: one delivery row
// + one queued job each. Fire-and-forget (audit.Log style) — a webhook hiccup
// must never fail the caller's work.
func Emit(ctx context.Context, q *sqlc.Queries, orgID, eventType string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		slog.ErrorContext(ctx, "webhook emit marshal", "type", eventType, "error", err)
		return
	}
	var dataMap map[string]any
	if err := json.Unmarshal(raw, &dataMap); err != nil {
		slog.ErrorContext(ctx, "webhook emit shape", "type", eventType, "error", err)
		return
	}
	env, err := json.Marshal(Envelope{
		Type: eventType, OccurredAt: time.Now().UTC().Format(time.RFC3339), Data: dataMap,
	})
	if err != nil {
		slog.ErrorContext(ctx, "webhook emit envelope", "type", eventType, "error", err)
		return
	}

	endpoints, err := q.ListActiveEndpointsForEvent(ctx, sqlc.ListActiveEndpointsForEventParams{
		ClerkOrgID: orgID, EventType: eventType,
	})
	if err != nil {
		slog.ErrorContext(ctx, "webhook emit lookup", "type", eventType, "org", orgID, "error", err)
		return
	}
	for _, ep := range endpoints {
		d, err := q.InsertWebhookDelivery(ctx, sqlc.InsertWebhookDeliveryParams{
			EndpointID: ep.ID, ClerkOrgID: orgID, EventType: eventType, Payload: env,
		})
		if err != nil {
			slog.ErrorContext(ctx, "webhook delivery insert", "type", eventType, "endpoint", ep.ID, "error", err)
			continue
		}
		if err := jobs.Enqueue(ctx, q, jobs.KindWebhookDeliver, jobs.WebhookDeliverPayload{DeliveryID: d.ID}); err != nil {
			slog.ErrorContext(ctx, "webhook delivery enqueue", "delivery", d.ID, "error", err)
		}
	}
}
