package web

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/flags"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlagEvaluatorSemantics(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_fl", "org_fl", "org:admin")
	ctx := t.Context()
	ev := flags.NewDBEvaluator(s.q, time.Minute)

	assert.False(t, ev.Enabled(ctx, "org_fl", "nonexistent"), "missing key → false")

	require.NoError(t, s.q.UpsertFeatureFlag(ctx, sqlc.UpsertFeatureFlagParams{Key: "beta", Description: "", Enabled: false, Rollout: 100}))
	assert.False(t, ev.Enabled(ctx, "org_fl", "beta"), "disabled → false")

	require.NoError(t, s.q.SetFeatureFlagEnabled(ctx, sqlc.SetFeatureFlagEnabledParams{Key: "beta", Enabled: true}))
	ev2 := flags.NewDBEvaluator(s.q, time.Minute) // fresh: bypass the 30s cache
	assert.True(t, ev2.Enabled(ctx, "org_fl", "beta"), "enabled + rollout 100 → true")

	// Rollout 0 → nobody (even enabled).
	require.NoError(t, s.q.SetFeatureFlagRollout(ctx, sqlc.SetFeatureFlagRolloutParams{Key: "beta", Rollout: 0}))
	ev3 := flags.NewDBEvaluator(s.q, time.Minute)
	assert.False(t, ev3.Enabled(ctx, "org_fl", "beta"), "rollout 0 → false")

	// Rollout 50: deterministic per org — same org twice agrees.
	require.NoError(t, s.q.SetFeatureFlagRollout(ctx, sqlc.SetFeatureFlagRolloutParams{Key: "beta", Rollout: 50}))
	ev4 := flags.NewDBEvaluator(s.q, time.Minute)
	first := ev4.Enabled(ctx, "org_fl", "beta")
	assert.Equal(t, first, ev4.Enabled(ctx, "org_fl", "beta"), "bucket is deterministic")
	// Across many orgs the split is neither all-on nor all-off.
	on := 0
	for i := range 40 {
		if ev4.Enabled(ctx, "org_bucket_"+string(rune('a'+i%26))+strconv.Itoa(i), "beta") {
			on++
		}
	}
	assert.Greater(t, on, 0, "some orgs in the 50% bucket")
	assert.Less(t, on, 40, "some orgs out of the 50% bucket")

	// Override wins over everything (even rollout 0).
	require.NoError(t, s.q.UpsertFlagOverride(ctx, sqlc.UpsertFlagOverrideParams{FlagKey: "beta", OrgID: "org_fl", Enabled: true}))
	assert.True(t, ev4.Enabled(ctx, "org_fl", "beta"), "org override on wins over rollout 0")
	require.NoError(t, s.q.UpsertFlagOverride(ctx, sqlc.UpsertFlagOverrideParams{FlagKey: "beta", OrgID: "org_fl", Enabled: false}))
	require.NoError(t, s.q.SetFeatureFlagRollout(ctx, sqlc.SetFeatureFlagRolloutParams{Key: "beta", Rollout: 100}))
	ev5 := flags.NewDBEvaluator(s.q, time.Minute)
	assert.False(t, ev5.Enabled(ctx, "org_fl", "beta"), "org override off wins over full rollout")
}

func TestAdminFlagsRequiresAdmin(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_fla", "org_fla", "org:admin")
	code, _, _ := serve(t, s, "GET", "/admin/flags", nil, nil, sessionCookie("user_fla", "org_fla", "org:admin"))
	assert.Equal(t, http.StatusForbidden, code)
}
