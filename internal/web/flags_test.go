package web

import (
	"net/http"
	"net/url"
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

func TestAdminFlagCreateAndDuplicate(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_fc", "org_fc")

	form := url.Values{"key": []string{"Bad Key!"}, "description": []string{"x"}, "rollout": []string{"100"}}
	code, _, body := postForm(t, s, "/admin/flags", form, sessionCookie("user_fc", "org_fc", "org:admin"))
	assert.Equal(t, http.StatusUnprocessableEntity, code, "invalid key rejected")
	assert.Contains(t, body, "alert-danger")

	form = url.Values{"key": []string{"flag-e2e-new"}, "description": []string{"created by test"}, "rollout": []string{"100"}}
	code, _, _ = postForm(t, s, "/admin/flags", form, sessionCookie("user_fc", "org_fc", "org:admin"))
	assert.Equal(t, http.StatusOK, code)
	flag, err := s.q.GetFeatureFlag(t.Context(), "flag-e2e-new")
	require.NoError(t, err)
	assert.False(t, flag.Enabled, "flags are created off")
	assert.EqualValues(t, 100, flag.Rollout)

	// Duplicate key → 422, values kept.
	code, _, body = postForm(t, s, "/admin/flags", form, sessionCookie("user_fc", "org_fc", "org:admin"))
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Contains(t, body, "already exists")
}

func TestAdminFlagDetailAndOverrides(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_fo", "org_fo")
	seedOrg(t, s, "org_fo2", "Override Org")
	require.NoError(t, s.q.UpsertFeatureFlag(t.Context(), sqlc.UpsertFeatureFlagParams{Key: "flag_ov", Description: "", Enabled: false, Rollout: 100}))
	s.invalidateFlagCache()
	cookie := sessionCookie("user_fo", "org_fo", "org:admin")

	// Detail renders with the add-override form and no overrides.
	code, _, body := serve(t, s, "GET", "/admin/flags/flag_ov", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `data-testid="flag-override-form"`)
	assert.Contains(t, body, `data-testid="flag-overrides"`)

	// Set an ON override for org_fo2 (global is off): evaluator flips for
	// that org IMMEDIATELY (overrides are uncached).
	form := url.Values{"org": []string{"org_fo2"}, "state": []string{"on"}}
	code, _, _ = postForm(t, s, "/admin/flags/flag_ov/overrides", form, cookie)
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, s.flags.Enabled(t.Context(), "org_fo2", "flag_ov"), "override wins over global off")
	assert.False(t, s.flags.Enabled(t.Context(), "org_other", "flag_ov"), "other orgs keep global")

	code, _, body = serve(t, s, "GET", "/admin/flags/flag_ov", nil, nil, cookie)
	assert.Contains(t, body, `data-testid="flag-override-org_fo2"`)

	// Remove the override → back to global.
	code, _, _ = postForm(t, s, "/admin/flags/flag_ov/overrides/org_fo2/delete", nil, cookie)
	assert.Equal(t, http.StatusOK, code)
	assert.False(t, s.flags.Enabled(t.Context(), "org_fo2", "flag_ov"), "org follows global after removal")
}

func TestAdminFlagDeleteCascadesOverrides(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_fd", "org_fd")
	require.NoError(t, s.q.UpsertFeatureFlag(t.Context(), sqlc.UpsertFeatureFlagParams{Key: "flag_del", Description: "", Enabled: true, Rollout: 100}))
	require.NoError(t, s.q.UpsertFlagOverride(t.Context(), sqlc.UpsertFlagOverrideParams{FlagKey: "flag_del", OrgID: "org_fd", Enabled: false}))
	s.invalidateFlagCache()
	cookie := sessionCookie("user_fd", "org_fd", "org:admin")

	code, _, _ := postForm(t, s, "/admin/flags/flag_del/delete", nil, cookie)
	assert.Equal(t, http.StatusOK, code)

	_, err := s.q.GetFeatureFlag(t.Context(), "flag_del")
	require.Error(t, err, "flag row deleted")
	overrides, err := s.q.ListFlagOverridesByFlag(t.Context(), "flag_del")
	require.NoError(t, err)
	assert.Empty(t, overrides, "FK cascade removed the override rows")
	assert.False(t, s.flags.Enabled(t.Context(), "org_fd", "flag_del"), "evaluator stops serving the deleted flag (invalidated)")
}
