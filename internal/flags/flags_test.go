package flags

import (
	"context"
	"hash/fnv"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/db/testdb"
	"github.com/stretchr/testify/require"
)

// insertFlag writes a flag row BEFORE the evaluator's first evaluation, so
// the 30s flag-row cache is built with the row present. Overrides are read
// per call (no cache) and can be changed at any point.
func insertFlag(t *testing.T, ctx context.Context, q *sqlc.Queries, key string, enabled bool, rollout int32) {
	t.Helper()
	require.NoError(t, q.UpsertFeatureFlag(ctx, sqlc.UpsertFeatureFlagParams{
		Key:         key,
		Description: "test flag",
		Enabled:     enabled,
		Rollout:     rollout,
	}))
}

func freshEvaluator(q *sqlc.Queries) *DBEvaluator {
	return NewDBEvaluator(q, 30*time.Second)
}

func TestDBEvaluatorMissingKey(t *testing.T) {
	_, q := testdb.Open(t, "flags")
	ctx := context.Background()
	e := freshEvaluator(q)
	require.False(t, e.Enabled(ctx, "org_any", "flag_never_created"))
}

func TestDBEvaluatorDisabledFlag(t *testing.T) {
	_, q := testdb.Open(t, "flags")
	ctx := context.Background()
	insertFlag(t, ctx, q, "flag_disabled", false, 100)
	e := freshEvaluator(q)
	require.False(t, e.Enabled(ctx, "org_any", "flag_disabled"))
}

func TestDBEvaluatorRolloutBounds(t *testing.T) {
	_, q := testdb.Open(t, "flags")
	ctx := context.Background()
	insertFlag(t, ctx, q, "flag_rollout_zero", true, 0)
	insertFlag(t, ctx, q, "flag_rollout_full", true, 100)
	e := freshEvaluator(q)
	require.False(t, e.Enabled(ctx, "org_any", "flag_rollout_zero"))
	require.True(t, e.Enabled(ctx, "org_any", "flag_rollout_full"))
}

func TestDBEvaluatorOverrideWins(t *testing.T) {
	_, q := testdb.Open(t, "flags")
	ctx := context.Background()
	// flag_overrides references orgs — the overridden org must exist.
	_, err := q.UpsertOrg(ctx, sqlc.UpsertOrgParams{ClerkOrgID: "org_ovr", Name: "Override Org", Slug: "override-org", ImageUrl: ""})
	require.NoError(t, err)
	// Globally disabled; override turns it ON for this org.
	insertFlag(t, ctx, q, "flag_global_off", false, 0)
	// Globally enabled at 100%; override turns it OFF for this org.
	insertFlag(t, ctx, q, "flag_global_on", true, 100)
	require.NoError(t, q.UpsertFlagOverride(ctx, sqlc.UpsertFlagOverrideParams{
		FlagKey: "flag_global_off", ClerkOrgID: "org_ovr", Enabled: true,
	}))
	require.NoError(t, q.UpsertFlagOverride(ctx, sqlc.UpsertFlagOverrideParams{
		FlagKey: "flag_global_on", ClerkOrgID: "org_ovr", Enabled: false,
	}))

	e := freshEvaluator(q)
	// Override wins both directions for the overridden org...
	require.True(t, e.Enabled(ctx, "org_ovr", "flag_global_off"))
	require.False(t, e.Enabled(ctx, "org_ovr", "flag_global_on"))
	// ...while other orgs still see the global state.
	require.False(t, e.Enabled(ctx, "org_other", "flag_global_off"))
	require.True(t, e.Enabled(ctx, "org_other", "flag_global_on"))
}

func TestDBEvaluatorFNVDeterminism(t *testing.T) {
	_, q := testdb.Open(t, "flags")
	ctx := context.Background()
	insertFlag(t, ctx, q, "flag_determinism", true, 50)
	e := freshEvaluator(q)

	first := e.Enabled(ctx, "org_det", "flag_determinism")
	for range 20 {
		require.Equal(t, first, e.Enabled(ctx, "org_det", "flag_determinism"))
	}
	// A fresh evaluator (cold cache) agrees: bucketing is a pure function of
	// (org, key), not of cache state.
	require.Equal(t, first, freshEvaluator(q).Enabled(ctx, "org_det", "flag_determinism"))
}

func TestDBEvaluatorFNVBucketBoundary(t *testing.T) {
	_, q := testdb.Open(t, "flags")
	ctx := context.Background()
	const org, key = "org_boundary", "flag_boundary"

	// Bucket recomputed independently from the implementation: FNV-1a 32 of
	// "org|key" mod 100. Pinned to a literal so an implementation change to
	// the bucketing scheme fails here loudly.
	h := fnv.New32a()
	_, _ = h.Write([]byte(org + "|" + key))
	bucket := int32(h.Sum32() % 100)
	require.Equal(t, int32(77), bucket)

	// rollout == bucket → off (bucket < rollout is false);
	// rollout == bucket+1 → on.
	insertFlag(t, ctx, q, key, true, bucket)
	require.False(t, freshEvaluator(q).Enabled(ctx, org, key))

	require.NoError(t, q.SetFeatureFlagRollout(ctx, sqlc.SetFeatureFlagRolloutParams{
		Key: key, Rollout: bucket + 1,
	}))
	require.True(t, freshEvaluator(q).Enabled(ctx, org, key))
}

func TestDBEvaluatorCanceledContextFailsClosed(t *testing.T) {
	_, q := testdb.Open(t, "flags")
	ctx := context.Background()
	// Even a globally-enabled 100% flag must evaluate false when the context
	// is dead: evaluation errors never widen access.
	insertFlag(t, ctx, q, "flag_canceled", true, 100)

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	e := freshEvaluator(q) // cold cache: both reads hit the canceled ctx
	require.False(t, e.Enabled(canceled, "org_any", "flag_canceled"))
	require.False(t, e.Enabled(canceled, "org_any", "flag_never_created"))
}

func TestDBEvaluatorInvalidateDropsCache(t *testing.T) {
	_, q := testdb.Open(t, "flagsinv")
	ctx := context.Background()
	insertFlag(t, ctx, q, "flag_inv", false, 100)

	e := freshEvaluator(q)
	require.False(t, e.Enabled(ctx, "org_inv", "flag_inv"))

	// Mutate BEHIND the evaluator's back; the 30s cache still serves old.
	require.NoError(t, q.SetFeatureFlagEnabled(ctx, sqlc.SetFeatureFlagEnabledParams{Key: "flag_inv", Enabled: true}))
	require.False(t, e.Enabled(ctx, "org_inv", "flag_inv"), "cached value before TTL")

	// Invalidate → next evaluation re-reads.
	e.Invalidate()
	require.True(t, e.Enabled(ctx, "org_inv", "flag_inv"), "fresh read after Invalidate")

	// And a deleted flag stops evaluating after Invalidate.
	require.NoError(t, q.DeleteFeatureFlag(ctx, "flag_inv"))
	require.True(t, e.Enabled(ctx, "org_inv", "flag_inv"), "still cached")
	e.Invalidate()
	require.False(t, e.Enabled(ctx, "org_inv", "flag_inv"), "missing key after Invalidate")
}
