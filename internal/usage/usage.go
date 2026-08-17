// Package usage is the metering seam: fire-and-forget local recording of
// usage events (audit.Log style), flushed to Polar by the usage.flush
// schedule. Meters live in billing.Plan; enforcement reads
// SumUsageByNameSince.
package usage

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
)

// Record writes one usage event. Fire-and-forget: errors are logged, never
// returned — metering must never fail the caller's work. externalID is the
// caller's own dedup hint ("" = none); the flush job sends Polar
// "ue-<usage_events.id>" as the dedup key.
func Record(ctx context.Context, q *sqlc.Queries, orgID, name string, value int64, externalID string, md map[string]any) {
	raw, err := json.Marshal(md)
	if err != nil {
		raw = []byte(`{}`)
	}
	_, err = q.InsertUsageEvent(ctx, sqlc.InsertUsageEventParams{
		ClerkOrgID: orgID, Name: name, Value: value, Metadata: raw, ExternalID: externalID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "usage record failed", "name", name, "org", orgID, "error", err)
	}
}
