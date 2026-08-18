package audit_test

import (
	"context"
	"testing"

	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/db/testdb"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// audit.Log is fire-and-forget: it returns nothing, so the contract is what
// lands in (or doesn't land in) the table.
func TestLogWritesRowWithMetadata(t *testing.T) {
	_, q := testdb.Open(t, "audit")
	ctx := context.Background()
	_, err := q.UpsertOrg(ctx, sqlc.UpsertOrgParams{ClerkOrgID: "org_a", Name: "A", Slug: "a", ImageUrl: ""})
	require.NoError(t, err)
	_, err = q.UpsertUser(ctx, sqlc.UpsertUserParams{ClerkUserID: "user_a", Email: "a@example.com", Name: "A", AvatarUrl: ""})
	require.NoError(t, err)

	audit.Log(ctx, q, "org_a", "user_a", "project.created", map[string]any{"id": 7})

	rows, err := q.ListAuditByOrg(ctx, sqlc.ListAuditByOrgParams{
		ClerkOrgID: pgtype.Text{String: "org_a", Valid: true}, Limit: 10, Offset: 0,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "project.created", rows[0].Action)
	assert.Equal(t, "a@example.com", rows[0].ActorEmail, "actor email join resolves")
	assert.JSONEq(t, `{"id":7}`, string(rows[0].Metadata), "metadata roundtrips as JSONB")
}

func TestLogEmptyOrgUserWritesNullColumns(t *testing.T) {
	_, q := testdb.Open(t, "audit2")
	ctx := context.Background()

	audit.Log(ctx, q, "", "", "system.tick", nil)

	rows, err := q.ListAuditAll(ctx, sqlc.ListAuditAllParams{Filter: "system.tick", Off: 0, Lim: 10})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.False(t, rows[0].ClerkOrgID.Valid, "empty org lands as NULL")
	assert.False(t, rows[0].ClerkUserID.Valid, "empty user lands as NULL")
}

func TestLogCanceledContextNoPanicNoReturn(t *testing.T) {
	_, q := testdb.Open(t, "audit3")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Fire-and-forget: no panic, no error surface — the row simply won't land.
	assert.NotPanics(t, func() {
		audit.Log(ctx, q, "org_x", "user_x", "canceled.write", nil)
	})
}
