// Package notify is the in-app notification seam: fire-and-forget row
// inserts (audit.Log style), consumed by the sidebar badge and the
// notifications page. Broadcasts fan out to one row per member at send time —
// never rows shared across users.
package notify

import (
	"context"
	"errors"
	"log/slog"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

// Kinds is the catalog of in-app notification kinds the product emits. The
// preferences page renders one row per entry; keep in sync with call sites
// (welcome, payment_failed, export.ready, webhook.failed).
var Kinds = []string{"welcome", "payment_failed", "export.ready", "webhook.failed"}

// Send notifies ONE user, honoring their per-kind preference: an explicit
// in_app = false row mutes the kind; an absent row means default-on. Errors
// are logged, never returned: a lost notification must never fail the
// caller's work.
func Send(ctx context.Context, q *sqlc.Queries, orgID, userID, kind, title, body, url string) {
	pref, err := q.GetNotificationPreference(ctx, sqlc.GetNotificationPreferenceParams{
		ClerkUserID: userID, Kind: kind,
	})
	switch {
	case err == nil && !pref.InApp:
		return // muted by preference
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		// A prefs hiccup must not silently drop notifications: log, then send.
		slog.ErrorContext(ctx, "notification preference lookup failed", "kind", kind, "user", userID, "error", err)
	}
	_, err = q.InsertNotification(ctx, sqlc.InsertNotificationParams{
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
