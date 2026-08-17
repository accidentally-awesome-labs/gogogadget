// Package notify is the in-app notification seam: fire-and-forget row
// inserts (audit.Log style), consumed by the sidebar badge and the
// notifications page. Broadcasts fan out to one row per member at send time —
// never rows shared across users.
package notify

import (
	"context"
	"log/slog"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
)

// Send notifies ONE user. Errors are logged, never returned: a lost
// notification must never fail the caller's work.
func Send(ctx context.Context, q *sqlc.Queries, orgID, userID, kind, title, body, url string) {
	_, err := q.InsertNotification(ctx, sqlc.InsertNotificationParams{
		ClerkOrgID: orgID, ClerkUserID: userID,
		Kind: kind, Title: title, Body: body, Url: url,
	})
	if err != nil {
		slog.ErrorContext(ctx, "notification send failed", "kind", kind, "user", userID, "error", err)
	}
}

// SendOrg fans out to every member of the org.
func SendOrg(ctx context.Context, q *sqlc.Queries, orgID, kind, title, body, url string) {
	members, err := q.ListMembersByOrg(ctx, orgID)
	if err != nil {
		slog.ErrorContext(ctx, "notification org fan-out failed", "kind", kind, "org", orgID, "error", err)
		return
	}
	for _, m := range members {
		Send(ctx, q, orgID, m.ClerkUserID, kind, title, body, url)
	}
}
