// Package audit writes the org-scoped audit trail. Logging is fire-and-forget:
// an audit failure is logged, never allowed to fail the request.
package audit

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

// Log records one action. orgID/userID may be empty (system actions).
func Log(ctx context.Context, q *sqlc.Queries, orgID, userID, action string, metadata map[string]any) {
	raw, err := json.Marshal(metadata)
	if err != nil {
		raw = []byte(`{}`)
	}
	_, err = q.InsertAuditLog(ctx, sqlc.InsertAuditLogParams{
		OrgID:  pgtype.Text{String: orgID, Valid: orgID != ""},
		UserID: pgtype.Text{String: userID, Valid: userID != ""},
		Action:      action,
		Metadata:    raw,
	})
	if err != nil {
		slog.ErrorContext(ctx, "audit log failed", "action", action, "error", err)
	}
}
