package jobs

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedUsageOrg(t *testing.T, q *sqlc.Queries, orgID string) {
	t.Helper()
	_, err := q.UpsertOrg(context.Background(), sqlc.UpsertOrgParams{ClerkOrgID: orgID, Name: orgID, Slug: orgID, ImageUrl: ""})
	require.NoError(t, err)
}

func insertUsage(t *testing.T, q *sqlc.Queries, orgID, name string, value int64) sqlc.UsageEvent {
	t.Helper()
	e, err := q.InsertUsageEvent(context.Background(), sqlc.InsertUsageEventParams{
		ClerkOrgID: orgID, Name: name, Value: value, Metadata: []byte(`{}`), ExternalID: "",
	})
	require.NoError(t, err)
	return e
}

// ageUsage pushes created_at past the 60s claim grace window.
func ageUsage(t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Helper()
	_, err := pool.Exec(context.Background(), "UPDATE usage_events SET created_at = $1 WHERE id = $2", time.Now().Add(-2*time.Minute), id)
	require.NoError(t, err)
}

func TestUsageFlushNoOpWithoutBilling(t *testing.T) {
	pool, q := testSetup(t)
	w := testWorker(q, t.TempDir()) // Billing nil
	seedUsageOrg(t, q, "org_u1")
	e := insertUsage(t, q, "org_u1", "ai_tokens", 100)
	ageUsage(t, pool, e.ID)

	require.NoError(t, w.flushUsage(context.Background(), SchedulePayload{}))

	var flushed bool
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT flushed_at IS NOT NULL FROM usage_events WHERE id = $1", e.ID).Scan(&flushed))
	assert.False(t, flushed, "events stay local when billing is unconfigured")
}

func TestUsageFlushIngestsAndMarks(t *testing.T) {
	pool, q := testSetup(t)
	w := testWorker(q, t.TempDir())
	mock := &billing.MockClient{}
	w.Billing = mock
	seedUsageOrg(t, q, "org_u2")

	e1 := insertUsage(t, q, "org_u2", "ai_tokens", 100)
	e2 := insertUsage(t, q, "org_u2", "ai_tokens", 250)
	fresh := insertUsage(t, q, "org_u2", "ai_tokens", 1) // inside the 60s grace window — NOT claimed
	ageUsage(t, pool, e1.ID)
	ageUsage(t, pool, e2.ID)

	require.NoError(t, w.flushUsage(context.Background(), SchedulePayload{}))

	require.Len(t, mock.Ingested, 1, "one ingest call for the org's batch")
	call := mock.Ingested[0]
	assert.Equal(t, "org_u2", call.Customer)
	require.Len(t, call.Events, 2)
	ids := []string{call.Events[0].ExternalID, call.Events[1].ExternalID}
	assert.Contains(t, ids, "ue-"+strconv.FormatInt(e1.ID, 10))
	assert.Contains(t, ids, "ue-"+strconv.FormatInt(e2.ID, 10))

	for _, id := range []int64{e1.ID, e2.ID} {
		var flushed bool
		require.NoError(t, pool.QueryRow(context.Background(), "SELECT flushed_at IS NOT NULL FROM usage_events WHERE id = $1", id).Scan(&flushed))
		assert.True(t, flushed)
	}
	var freshFlushed bool
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT flushed_at IS NOT NULL FROM usage_events WHERE id = $1", fresh.ID).Scan(&freshFlushed))
	assert.False(t, freshFlushed, "grace window row not claimed")
}

var errIngest = errors.New("polar down")

func TestUsageFlushIngestFailureUnflushes(t *testing.T) {
	pool, q := testSetup(t)
	w := testWorker(q, t.TempDir())
	mock := &billing.MockClient{IngestErr: errIngest}
	w.Billing = mock
	seedUsageOrg(t, q, "org_u3")

	e := insertUsage(t, q, "org_u3", "ai_tokens", 42)
	ageUsage(t, pool, e.ID)

	err := w.flushUsage(context.Background(), SchedulePayload{})
	require.Error(t, err, "ingest failure surfaces (job retries)")

	var flushed bool
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT flushed_at IS NOT NULL FROM usage_events WHERE id = $1", e.ID).Scan(&flushed))
	assert.False(t, flushed, "failed batch returns to the pool")
}

func TestSumUsageByNameSince(t *testing.T) {
	pool, q := testSetup(t)
	ctx := context.Background()
	seedUsageOrg(t, q, "org_u4")
	insertUsage(t, q, "org_u4", "ai_tokens", 10)
	insertUsage(t, q, "org_u4", "ai_tokens", 20)
	insertUsage(t, q, "org_u4", "other", 999)

	since := pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}
	sum, err := q.SumUsageByNameSince(ctx, sqlc.SumUsageByNameSinceParams{ClerkOrgID: "org_u4", Name: "ai_tokens", CreatedAt: since})
	require.NoError(t, err)
	assert.Equal(t, int64(30), sum)

	// Since-boundary excludes older rows.
	old := insertUsage(t, q, "org_u4", "ai_tokens", 500)
	_, err = pool.Exec(ctx, "UPDATE usage_events SET created_at = $1 WHERE id = $2", time.Now().Add(-48*time.Hour), old.ID)
	require.NoError(t, err)
	sum, err = q.SumUsageByNameSince(ctx, sqlc.SumUsageByNameSinceParams{ClerkOrgID: "org_u4", Name: "ai_tokens", CreatedAt: since})
	require.NoError(t, err)
	assert.Equal(t, int64(30), sum, "older rows excluded")
}
