package db_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/db"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testPool migrates up against TEST_DATABASE_URL (default gogogadget_test) and
// skips when Postgres is unreachable. CI provides the database.
func testPool(t *testing.T) (*pgxpool.Pool, *sqlc.Queries) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5432/gogogadget_test?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := db.Open(ctx, dsn)
	if err != nil {
		t.Skipf("TEST_DATABASE_URL unreachable: %v", err)
	}
	require.NoError(t, db.Migrate(ctx, pool))
	t.Cleanup(pool.Close)
	return pool, sqlc.New(pool)
}

func TestMigrateUpDown(t *testing.T) {
	pool, _ := testPool(t)
	ctx := context.Background()
	require.NoError(t, db.MigrateDown(ctx, pool))
	require.NoError(t, db.Migrate(ctx, pool)) // back up for other tests
}

func TestRoundtripEveryTable(t *testing.T) {
	_, q := testPool(t)
	ctx := context.Background()

	// users
	u, err := q.UpsertUser(ctx, sqlc.UpsertUserParams{
		ClerkUserID: "user_rt1", Email: "rt@example.com", Name: "RT", AvatarUrl: "",
	})
	require.NoError(t, err)
	assert.Equal(t, "rt@example.com", string(u.Email))
	gotU, err := q.GetUserByClerkID(ctx, "user_rt1")
	require.NoError(t, err)
	assert.Equal(t, u.ClerkUserID, gotU.ClerkUserID)

	// orgs
	_, err = q.UpsertOrg(ctx, sqlc.UpsertOrgParams{
		ClerkOrgID: "org_rt1", Name: "RT Org", Slug: "rt-org", ImageUrl: "",
	})
	require.NoError(t, err)
	_, err = q.GetOrgByClerkID(ctx, "org_rt1")
	require.NoError(t, err)

	// memberships
	require.NoError(t, q.UpsertMembership(ctx, sqlc.UpsertMembershipParams{
		ClerkOrgID: "org_rt1", ClerkUserID: "user_rt1", Role: "org:admin",
	}))
	members, err := q.ListMembersByOrg(ctx, "org_rt1")
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.Equal(t, "org:admin", members[0].Role)

	// subscriptions (upsert conflict target is clerk_org_id)
	sub, err := q.UpsertSubscription(ctx, sqlc.UpsertSubscriptionParams{
		ClerkOrgID: "org_rt1", PolarSubscriptionID: pgtype.Text{String: "sub_1", Valid: true},
		PolarCustomerID: "cust_1", ProductKey: "pro", Status: "active",
	})
	require.NoError(t, err)
	sub2, err := q.UpsertSubscription(ctx, sqlc.UpsertSubscriptionParams{
		ClerkOrgID: "org_rt1", PolarSubscriptionID: pgtype.Text{String: "sub_2", Valid: true},
		PolarCustomerID: "cust_1", ProductKey: "team", Status: "trialing",
	})
	require.NoError(t, err)
	assert.Equal(t, sub.ID, sub2.ID, "resubscribe must overwrite the same org row")
	assert.Equal(t, "sub_2", sub2.PolarSubscriptionID.String)

	// webhook idempotency
	id1, err := q.InsertWebhookEvent(ctx, sqlc.InsertWebhookEventParams{ID: "wh_rt1", Provider: "clerk", EventType: "user.created"})
	require.NoError(t, err)
	assert.Equal(t, "wh_rt1", id1)
	_, err = q.InsertWebhookEvent(ctx, sqlc.InsertWebhookEventParams{ID: "wh_rt1", Provider: "clerk", EventType: "user.created"})
	require.ErrorIs(t, err, pgx.ErrNoRows, "duplicate delivery must be a no-op")

	// audit
	_, err = q.InsertAuditLog(ctx, sqlc.InsertAuditLogParams{
		ClerkOrgID:  pgtype.Text{String: "org_rt1", Valid: true},
		ClerkUserID: pgtype.Text{String: "user_rt1", Valid: true},
		Action:      "project.created",
		Metadata:    []byte(`{"name":"x"}`),
	})
	require.NoError(t, err)
	rows, err := q.ListAuditByOrg(ctx, sqlc.ListAuditByOrgParams{ClerkOrgID: pgtype.Text{String: "org_rt1", Valid: true}, Limit: 10, Offset: 0})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "rt@example.com", rows[0].ActorEmail)

	// projects
	p, err := q.CreateProject(ctx, sqlc.CreateProjectParams{ClerkOrgID: "org_rt1", Name: "Alpha"})
	require.NoError(t, err)
	_, err = q.UpdateProject(ctx, sqlc.UpdateProjectParams{ID: p.ID, ClerkOrgID: "org_rt1", Name: "Alpha 2"})
	require.NoError(t, err)
	found, err := q.ListProjectsByOrg(ctx, sqlc.ListProjectsByOrgParams{ClerkOrgID: "org_rt1", Column2: "lpha", Limit: 10, Offset: 0})
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, "Alpha 2", found[0].Name)
	// cross-org reads never leak
	_, err = q.GetProjectByID(ctx, sqlc.GetProjectByIDParams{ID: p.ID, ClerkOrgID: "org_other"})
	require.ErrorIs(t, err, pgx.ErrNoRows)

	// jobs
	jid, err := q.EnqueueJob(ctx, sqlc.EnqueueJobParams{Kind: "email.welcome", Payload: []byte(`{"to":"rt@example.com"}`)})
	require.NoError(t, err)
	job, err := q.ClaimJob(ctx)
	require.NoError(t, err)
	assert.Equal(t, jid, job.ID)
	assert.Equal(t, int32(1), job.Attempts)
	require.NoError(t, q.CompleteJob(ctx, jid))
	_, err = q.ClaimJob(ctx)
	require.ErrorIs(t, err, pgx.ErrNoRows, "completed job must not be reclaimed")

	// api tokens
	tid, err := q.InsertAPIToken(ctx, sqlc.InsertAPITokenParams{
		ClerkOrgID: "org_rt1", Name: "ci", TokenHash: "hash_rt1", Scope: "write",
	})
	require.NoError(t, err)
	tok, err := q.GetAPITokenByHash(ctx, "hash_rt1")
	require.NoError(t, err)
	assert.Equal(t, tid, tok.ID)
	require.NoError(t, q.RevokeAPIToken(ctx, sqlc.RevokeAPITokenParams{ID: tid, ClerkOrgID: "org_rt1"}))
	_, err = q.GetAPITokenByHash(ctx, "hash_rt1")
	require.ErrorIs(t, err, pgx.ErrNoRows, "revoked token must not authenticate")
}
