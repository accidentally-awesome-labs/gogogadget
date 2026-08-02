package billing

import (
	"context"
	"errors"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

// Entitled is THE subscription gate: whether a subscription row currently
// confers paid-plan access.
func Entitled(sub *sqlc.Subscription, now time.Time) bool {
	if sub == nil {
		return true // free plan
	}
	switch sub.Status {
	case "active", "trialing", "past_due":
		return true // past_due = grace + banner
	case "canceled":
		return sub.CurrentPeriodEnd.Valid && sub.CurrentPeriodEnd.Time.After(now)
	default:
		return false // unpaid, incomplete, incomplete_expired
	}
}

// CurrentPlan resolves the org's effective plan: a subscription row confers
// its product's plan only while Entitled; anything else is free. The Entitled
// gate inside CurrentPlan is the fix for the canonical bug (expired/revoked
// sub silently keeping paid limits).
func CurrentPlan(ctx context.Context, q *sqlc.Queries, orgID string, now time.Time) Plan {
	sub, err := q.GetSubscriptionByOrg(ctx, orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlanByKey("free")
	}
	if err != nil {
		return PlanByKey("free") // DB hiccup must never widen entitlements
	}
	if Entitled(&sub, now) {
		return PlanByKey(sub.ProductKey)
	}
	return PlanByKey("free")
}
