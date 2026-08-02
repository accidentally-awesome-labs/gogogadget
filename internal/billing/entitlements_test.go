package billing

import (
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
)

func TestEntitled(t *testing.T) {
	now := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	future := pgtype.Timestamptz{Time: now.Add(24 * time.Hour), Valid: true}
	past := pgtype.Timestamptz{Time: now.Add(-24 * time.Hour), Valid: true}

	sub := func(status string, end pgtype.Timestamptz) *sqlc.Subscription {
		return &sqlc.Subscription{Status: status, CurrentPeriodEnd: end}
	}

	assert.True(t, Entitled(nil, now), "no row = free plan = entitled")
	assert.True(t, Entitled(sub("active", pgtype.Timestamptz{}), now))
	assert.True(t, Entitled(sub("trialing", pgtype.Timestamptz{}), now))
	assert.True(t, Entitled(sub("past_due", pgtype.Timestamptz{}), now), "past_due = grace period")
	assert.True(t, Entitled(sub("canceled", future), now), "canceled but period remains")
	assert.False(t, Entitled(sub("canceled", past), now), "canceled and period elapsed")
	assert.False(t, Entitled(sub("canceled", pgtype.Timestamptz{}), now), "canceled with no period end")
	assert.False(t, Entitled(sub("unpaid", future), now))
	assert.False(t, Entitled(sub("incomplete", future), now))
	assert.False(t, Entitled(sub("incomplete_expired", future), now))
}

func TestPlanByKey(t *testing.T) {
	assert.Equal(t, "pro", PlanByKey("pro").Key)
	assert.Equal(t, "free", PlanByKey("unknown").Key, "unknown key falls back to free")
	assert.Equal(t, 3, PlanByKey("free").MaxProjects)
	assert.Equal(t, -1, PlanByKey("pro").MaxProjects)
	assert.Equal(t, 20, PlanByKey("pro").PriceUSDMonthly)
	assert.Equal(t, 50, PlanByKey("team").PriceUSDMonthly)
}
