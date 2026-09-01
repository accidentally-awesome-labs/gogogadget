package schedules_test

import (
	"context"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/db/testdb"
	"github.com/gogogadget/gogogadget/internal/schedules"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateRoundtrip(t *testing.T) {
	_, q := testdb.Open(t, "schedules")
	ctx := context.Background()

	// Org-bound schedule: org_id FK requires the org row.
	_, err := q.UpsertOrg(ctx, sqlc.UpsertOrgParams{
		OrgID: "org_sched", Name: "Sched Org", Slug: "sched-org", ImageUrl: "",
	})
	require.NoError(t, err)

	orgSched, err := schedules.Create(ctx, q, schedules.Schedule{
		Name:         "org-digest",
		Kind:         "email.digest",
		Payload:      map[string]any{"filter": "unread"},
		OrgID:   "org_sched",
		EverySeconds: 3600,
	})
	require.NoError(t, err)
	require.NotZero(t, orgSched.ID)

	// System-wide schedule (OrgID "" → NULL).
	sysSched, err := schedules.Create(ctx, q, schedules.Schedule{
		Name:         "system-sweep",
		Kind:         "jobs.sweep",
		Payload:      map[string]any{},
		EverySeconds: 60,
	})
	require.NoError(t, err)

	rows, err := q.ListSchedules(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 2) // ORDER BY name

	byName := map[string]sqlc.Schedule{}
	for _, r := range rows {
		byName[r.Name] = r
	}

	got := byName["org-digest"]
	assert.Equal(t, orgSched.ID, got.ID)
	assert.Equal(t, "email.digest", got.Kind)
	assert.JSONEq(t, `{"filter":"unread"}`, string(got.Payload))
	assert.True(t, got.OrgID.Valid)
	assert.Equal(t, "org_sched", got.OrgID.String)
	assert.Equal(t, int32(3600), got.EverySeconds)
	assert.True(t, got.Enabled)
	assert.True(t, got.NextRunAt.Valid) // COALESCE → now()

	got = byName["system-sweep"]
	assert.Equal(t, sysSched.ID, got.ID)
	assert.Equal(t, "jobs.sweep", got.Kind)
	assert.False(t, got.OrgID.Valid) // system-wide = NULL
	assert.Equal(t, int32(60), got.EverySeconds)
}

func TestCreateBelowMinimumIntervalRejected(t *testing.T) {
	_, q := testdb.Open(t, "schedules_min")
	ctx := context.Background()

	// every_seconds >= 60 is a table CHECK; Create returns errors (not
	// fire-and-forget), so the violation must surface to the caller.
	_, err := schedules.Create(ctx, q, schedules.Schedule{
		Name:         "too-fast",
		Kind:         "jobs.sweep",
		Payload:      map[string]any{},
		EverySeconds: 59,
	})
	require.Error(t, err)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "23514", pgErr.Code) // check_violation
	assert.Equal(t, "schedules_every_seconds_check", pgErr.ConstraintName)
}
