package usage_test

import (
	"context"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/db/testdb"
	"github.com/gogogadget/gogogadget/internal/usage"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Rows become listable only via the 60s-grace ClaimUsageBatch, so the
// observable read here is SumUsageByNameSince — the same read enforcement
// uses. Metadata marshaling is exercised by the audit tests (same pattern).
func sum(t *testing.T, q *sqlc.Queries, org, name string) int64 {
	t.Helper()
	n, err := q.SumUsageByNameSince(context.Background(), sqlc.SumUsageByNameSinceParams{
		OrgID: org, Name: name, CreatedAt: pgtype.Timestamptz{Time: time.Unix(0, 0), Valid: true},
	})
	require.NoError(t, err)
	return n
}

func TestRecordWritesUsageEvent(t *testing.T) {
	_, q := testdb.Open(t, "usage")
	ctx := context.Background()
	_, err := q.UpsertOrg(ctx, sqlc.UpsertOrgParams{OrgID: "org_u", Name: "U", Slug: "u", ImageUrl: ""})
	require.NoError(t, err)

	usage.Record(ctx, q, "org_u", "ai.tokens", 42, "ext-1", map[string]any{"model": "gpt"})

	assert.Equal(t, int64(42), sum(t, q, "org_u", "ai.tokens"), "event lands and sums")
	assert.Equal(t, int64(0), sum(t, q, "org_u", "other.meter"), "meters are independent")
}

func TestRecordUnmarshalableMetadataStillRecords(t *testing.T) {
	_, q := testdb.Open(t, "usage2")
	ctx := context.Background()
	_, err := q.UpsertOrg(ctx, sqlc.UpsertOrgParams{OrgID: "org_u2", Name: "U2", Slug: "u2", ImageUrl: ""})
	require.NoError(t, err)

	// json.Marshal(chan) fails → Record falls back to {} and still records.
	assert.NotPanics(t, func() {
		usage.Record(ctx, q, "org_u2", "storage.mb", 7, "", map[string]any{"ch": make(chan int)})
	})
	assert.Equal(t, int64(7), sum(t, q, "org_u2", "storage.mb"), "bad metadata degrades to {}, never drops the event")
}

func TestRecordCanceledContextNoPanic(t *testing.T) {
	_, q := testdb.Open(t, "usage3")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	assert.NotPanics(t, func() {
		usage.Record(ctx, q, "org_none", "x", 1, "", nil)
	})
}
