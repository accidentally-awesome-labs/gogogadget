package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
)

// flushUsage is the usage.flush dispatch: claim a batch of unflushed events
// (the claim marks them; failure un-marks), group by org, and ingest each
// org's events at Polar. Idempotent by construction: Polar dedups on
// external_id (we send ue-<usage_events.id>), so at-least-once retries are
// safe. No billing client → no-op (events stay local).
func (w *Worker) flushUsage(ctx context.Context, _ SchedulePayload) error {
	if w.Billing == nil {
		w.log.Debug("usage.flush: billing unconfigured — events stay local")
		return nil
	}
	for {
		batch, err := w.q.ClaimUsageBatch(ctx)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}

		byOrg := map[string][]sqlc.UsageEvent{}
		for _, e := range batch {
			byOrg[e.ClerkOrgID] = append(byOrg[e.ClerkOrgID], e)
		}
		var failed []int64
		for orgID, events := range byOrg {
			usageEvents := make([]billing.UsageEvent, 0, len(events))
			for _, e := range events {
				md := map[string]any{}
				_ = json.Unmarshal(e.Metadata, &md)
				usageEvents = append(usageEvents, billing.UsageEvent{
					Name: e.Name, ExternalID: "ue-" + strconv.FormatInt(e.ID, 10),
					Value: e.Value, Metadata: md,
				})
			}
			if err := w.Billing.IngestUsage(ctx, orgID, usageEvents); err != nil {
				w.log.Error("usage flush ingest", "org", orgID, "events", len(events), "error", err)
				for _, e := range events {
					failed = append(failed, e.ID)
				}
			}
		}
		if len(failed) > 0 {
			// Return failed rows to the pool; the next tick retries them.
			if err := w.q.UnflushUsageBatch(ctx, failed); err != nil {
				return fmt.Errorf("usage flush un-mark: %w", err)
			}
			return fmt.Errorf("usage flush: %d events failed", len(failed))
		}
		// Full batch succeeded — loop for the next one.
	}
}
