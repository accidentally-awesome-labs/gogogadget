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

// CurrentPlanWithCatalog resolves the org's effective plan using the selected
// immutable catalog. Database failures conservatively return free.
func CurrentPlanWithCatalog(ctx context.Context, q *sqlc.Queries, orgID string, now time.Time, catalog PlanCatalog) Plan {
	if catalog == nil {
		catalog = DefaultPlanCatalog()
	}
	sub, err := q.GetSubscriptionByOrg(ctx, orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return catalog.ByKey("free")
	}
	if err != nil {
		return catalog.ByKey("free")
	}
	if Entitled(&sub, now) {
		return catalog.ByKey(sub.ProductKey)
	}
	return catalog.ByKey("free")
}

func CurrentPlan(ctx context.Context, q *sqlc.Queries, orgID string, now time.Time) Plan {
	return CurrentPlanWithCatalog(ctx, q, orgID, now, DefaultPlanCatalog())
}
