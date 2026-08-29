// Cleanup sweep for the API idempotency cache. Owned by system/api.
package jobs

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// IdempotencyRetention is how long a stored API response stays replayable.
// Long enough for any sane client retry schedule; short enough that the table
// is a cache, not an archive.
const IdempotencyRetention = 24 * time.Hour

func (w *Worker) janitorIdempotencyKeys(ctx context.Context) error {
	cutoff := pgtype.Timestamptz{Time: time.Now().Add(-IdempotencyRetention), Valid: true}
	n, err := w.q.DeleteOldIdempotencyKeys(ctx, cutoff)
	if err != nil {
		return err
	}
	if n > 0 {
		w.log.Info("janitor idempotency_keys", "deleted", n)
	}
	return nil
}
