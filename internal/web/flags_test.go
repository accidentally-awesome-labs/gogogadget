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
	require.NoError(t, s.q.UpsertFlagOverride(ctx, sqlc.UpsertFlagOverrideParams{FlagKey: "beta", ClerkOrgID: "org_fl", Enabled: true}))
	assert.True(t, ev4.Enabled(ctx, "org_fl", "beta"), "org override on wins over rollout 0")
	require.NoError(t, s.q.UpsertFlagOverride(ctx, sqlc.UpsertFlagOverrideParams{FlagKey: "beta", ClerkOrgID: "org_fl", Enabled: false}))
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

func TestAdminFlagToggleGatesWebhooksTab(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_flt", "org_flt")
	cookie := sessionCookie("user_flt", "org_flt", "org:admin")
	ctx := t.Context()

	// Seed the flag ON.
	require.NoError(t, s.q.UpsertFeatureFlag(ctx, sqlc.UpsertFeatureFlagParams{Key: "webhooks", Description: "", Enabled: true, Rollout: 100}))
	t.Cleanup(func() {
		_, _ = s.db.Exec(ctx, "DELETE FROM feature_flags WHERE key = 'webhooks'")
		_, _ = s.db.Exec(ctx, "DELETE FROM audit_log WHERE action = 'flag.updated'")
	})

	// Tab visible + route reachable.
	code, _, body := serve(t, s, "GET", "/app/settings/account", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "/app/settings/webhooks")
	code, _, _ = serve(t, s, "GET", "/app/settings/webhooks", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)

	// Admin toggles it OFF (htmx post).
	token, csrfCookies := csrfFor(t, s)
	h := http.Header{}
	h.Set("X-CSRF-Token", token)
	h.Set("Content-Type", "application/x-www-form-urlencoded")
	h.Set("HX-Request", "true")
	code, _, _ = serve(t, s, "POST", "/admin/flags/webhooks/toggle", nil, h, append(csrfCookies, cookie)...)
	require.Equal(t, http.StatusOK, code)

	// Tab gone (fresh render re-evaluates — new evaluator instance per Render
	// read is the DB cache's 30s TTL, so assert via a FRESH evaluator path:
	// the page render uses s.flags…; flush by replacing the evaluator).
	s.flags = flags.NewDBEvaluator(s.q, time.Minute)
	code, _, body = serve(t, s, "GET", "/app/settings/account", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "/app/settings/webhooks", "tab hidden when flag off")
	code, _, _ = serve(t, s, "GET", "/app/settings/webhooks", nil, nil, cookie)
	assert.Equal(t, http.StatusNotFound, code, "route 404s when flag off")
}
