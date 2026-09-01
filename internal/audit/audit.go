// Package audit writes the org-scoped audit trail. Logging is fire-and-forget:
// an audit failure is logged, never allowed to fail the request.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

// Log records one action. orgID/userID may be empty (system actions).
func Log(ctx context.Context, q *sqlc.Queries, orgID, userID, action string, metadata map[string]any) {
	if _, err := insert(ctx, q, orgID, userID, action, metadata); err != nil {
		slog.ErrorContext(ctx, "audit log failed", "action", action, "error", err)
	}
}

// LogAndEnqueue is the transactional integration point for audit export. The
// ledger insert succeeds before the exporter queue is touched; enqueue failure
// is returned for the caller's retry/dead-letter policy and can never erase the
// durable audit row.
func LogAndEnqueue(ctx context.Context, q *sqlc.Queries, outbox Outbox, orgID, userID, action string, metadata map[string]any) error {
	id, err := insert(ctx, q, orgID, userID, action, metadata)
	if err != nil {
		return err
	}
	if outbox == nil {
		return fmt.Errorf("audit export: outbox is required")
	}
	return outbox.Enqueue(ctx, Entry{ID: fmt.Sprint(id), OrgID: orgID, UserID: userID, Action: action, Metadata: metadata})
}

func insert(ctx context.Context, q *sqlc.Queries, orgID, userID, action string, metadata map[string]any) (int64, error) {
	if q == nil {
		return 0, fmt.Errorf("audit: queries are required")
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		raw = []byte(`{}`)
	}
	return q.InsertAuditLog(ctx, sqlc.InsertAuditLogParams{
		OrgID: pgtype.Text{String: orgID, Valid: orgID != ""}, UserID: pgtype.Text{String: userID, Valid: userID != ""}, Action: action, Metadata: raw,
	})
}
