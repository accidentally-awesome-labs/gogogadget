// Cleanup sweep for the audit log. Owned by system/audit.
package jobs

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// janitorAuditLog trims the audit log to the configured retention. Zero days
// means retain forever, which is the compliance-safe default: silently deleting
// an audit trail because a setting was unset would be the worse failure.
func (w *Worker) janitorAuditLog(ctx context.Context) error {
	if w.AuditRetentionDays <= 0 {
		return nil
	}
	cutoff := pgtype.Timestamptz{Time: time.Now().AddDate(0, 0, -w.AuditRetentionDays), Valid: true}
	n, err := w.q.DeleteOldAuditRows(ctx, cutoff)
	if err != nil {
		return err
	}
	if n > 0 {
		w.log.Info("janitor audit_log", "deleted", n, "retention_days", w.AuditRetentionDays)
	}
	return nil
}
