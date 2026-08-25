// Cleanup sweeps for the outbound-webhook tables. Owned by system/webhooks: the
// module that owns these tables owns their retention.
package jobs

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// janitorWebhookEvents drops delivered events after 30 days.
func (w *Worker) janitorWebhookEvents(ctx context.Context) error {
	return w.q.DeleteOldWebhookEvents(ctx)
}

// janitorWebhookSecrets clears rotated-out signing secrets once the grace window
// has passed, so a leaked old secret stops being accepted.
func (w *Worker) janitorWebhookSecrets(ctx context.Context) error {
	cutoff := pgtype.Timestamptz{Time: time.Now().Add(-WebhookRotationGrace), Valid: true}
	n, err := w.q.ClearExpiredPreviousSecrets(ctx, cutoff)
	if err != nil {
		return err
	}
	if n > 0 {
		w.log.Info("janitor webhook secrets", "cleared", n)
	}
	return nil
}
