package notify_test

import (
	"context"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/db/testdb"
	"github.com/gogogadget/gogogadget/internal/notify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedUserOrg(t *testing.T, q *sqlc.Queries, userID, orgID string) {
	t.Helper()
	ctx := context.Background()
	_, err := q.UpsertOrg(ctx, sqlc.UpsertOrgParams{OrgID: orgID, Name: orgID, Slug: orgID, ImageUrl: ""})
	require.NoError(t, err)
	_, err = q.UpsertUser(ctx, sqlc.UpsertUserParams{UserID: userID, Email: userID + "@example.com", Name: userID, AvatarUrl: ""})
	require.NoError(t, err)
	require.NoError(t, q.UpsertMembership(ctx, sqlc.UpsertMembershipParams{
		OrgID: orgID, UserID: userID, Role: "org:admin",
	}))
}

func countNotifications(t *testing.T, q *sqlc.Queries, orgID, userID string) int64 {
	t.Helper()
	n, err := q.CountNotificationsByUser(context.Background(), sqlc.CountNotificationsByUserParams{
		OrgID: orgID, UserID: userID,
	})
	require.NoError(t, err)
	return n
}

func TestSendWritesRow(t *testing.T) {
	_, q := testdb.Open(t, "notify")
	seedUserOrg(t, q, "user_n", "org_n")
	ctx := context.Background()

	notify.Send(ctx, q, "org_n", "user_n", "welcome", "Hello", "Body", "/app")

	require.Equal(t, int64(1), countNotifications(t, q, "org_n", "user_n"))
	rows, err := q.ListNotificationsByUser(ctx, sqlc.ListNotificationsByUserParams{
		OrgID: "org_n", UserID: "user_n", Limit: 10, Offset: 0,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "welcome", rows[0].Kind)
	assert.Equal(t, "Hello", rows[0].Title)
	assert.Equal(t, "/app", rows[0].Url)
}

func TestSendMutedKindWritesNothing(t *testing.T) {
	_, q := testdb.Open(t, "notify2")
	seedUserOrg(t, q, "user_n2", "org_n2")
	ctx := context.Background()

	// Explicit opt-out row mutes the kind…
	require.NoError(t, q.UpsertNotificationPreference(ctx, sqlc.UpsertNotificationPreferenceParams{
		UserID: "user_n2", Kind: "welcome", InApp: false,
	}))
	notify.Send(ctx, q, "org_n2", "user_n2", "welcome", "Hello", "Body", "")
	assert.Zero(t, countNotifications(t, q, "org_n2", "user_n2"), "muted kind must not land")

	// …while kinds without a preference row stay default-on…
	notify.Send(ctx, q, "org_n2", "user_n2", "export.ready", "Ready", "", "")
	assert.Equal(t, int64(1), countNotifications(t, q, "org_n2", "user_n2"))

	// …and a row set back to true un-mutes.
	require.NoError(t, q.UpsertNotificationPreference(ctx, sqlc.UpsertNotificationPreferenceParams{
		UserID: "user_n2", Kind: "welcome", InApp: true,
	}))
	notify.Send(ctx, q, "org_n2", "user_n2", "welcome", "Hello", "", "")
	assert.Equal(t, int64(2), countNotifications(t, q, "org_n2", "user_n2"))
}

func TestSendOrgFansOutPerMember(t *testing.T) {
	_, q := testdb.Open(t, "notify3")
	ctx := context.Background()
	seedUserOrg(t, q, "user_m1", "org_o")
	seedUserOrg(t, q, "user_m2", "org_o")
	// Mute ONE member's kind: fan-out honors per-user prefs.
	require.NoError(t, q.UpsertNotificationPreference(ctx, sqlc.UpsertNotificationPreferenceParams{
		UserID: "user_m2", Kind: "payment_failed", InApp: false,
	}))

	notify.SendOrg(ctx, q, "org_o", "payment_failed", "Failed", "", "")

	assert.Equal(t, int64(1), countNotifications(t, q, "org_o", "user_m1"), "member without prefs receives")
	assert.Zero(t, countNotifications(t, q, "org_o", "user_m2"), "muted member does not")
}

func TestSendCanceledContextSilent(t *testing.T) {
	_, q := testdb.Open(t, "notify4")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	assert.NotPanics(t, func() {
		notify.Send(ctx, q, "org_x", "user_x", "welcome", "t", "b", "")
		notify.SendOrg(ctx, q, "org_x", "welcome", "t", "b", "")
	})
}
