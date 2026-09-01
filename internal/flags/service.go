package flags

import (
	"context"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
)

var _ Service = (*DBEvaluator)(nil)

func (e *DBEvaluator) List(ctx context.Context) ([]Flag, error) {
	rows, err := e.q.ListFeatureFlags(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Flag, 0, len(rows))
	for _, r := range rows {
		out = append(out, Flag{Key: r.Key, Description: r.Description, Enabled: r.Enabled, Rollout: int(r.Rollout)})
	}
	return out, nil
}
func (e *DBEvaluator) Upsert(ctx context.Context, f Flag) error {
	if e == nil || e.q == nil {
		return fmt.Errorf("flags: queries are required")
	}
	if _, err := e.q.GetFeatureFlag(ctx, f.Key); err != nil {
		return e.q.UpsertFeatureFlag(ctx, sqlc.UpsertFeatureFlagParams{Key: f.Key, Description: f.Description, Enabled: f.Enabled, Rollout: int32(f.Rollout)})
	}
	if err := e.q.SetFeatureFlagEnabled(ctx, sqlc.SetFeatureFlagEnabledParams{Key: f.Key, Enabled: f.Enabled}); err != nil {
		return err
	}
	return e.q.SetFeatureFlagRollout(ctx, sqlc.SetFeatureFlagRolloutParams{Key: f.Key, Rollout: int32(f.Rollout)})
}
func (e *DBEvaluator) Delete(ctx context.Context, key string) error {
	return e.q.DeleteFeatureFlag(ctx, key)
}
func (e *DBEvaluator) ListOverrides(ctx context.Context, key string) ([]Override, error) {
	rows, err := e.q.ListFlagOverridesByFlag(ctx, key)
	if err != nil {
		return nil, err
	}
	out := make([]Override, 0, len(rows))
	for _, r := range rows {
		out = append(out, Override{OrgID: r.OrgID, Enabled: r.OverrideEnabled})
	}
	return out, nil
}
func (e *DBEvaluator) SetOverride(ctx context.Context, key, org string, enabled bool) error {
	return e.q.UpsertFlagOverride(ctx, sqlc.UpsertFlagOverrideParams{FlagKey: key, OrgID: org, Enabled: enabled})
}
func (e *DBEvaluator) DeleteOverride(ctx context.Context, key, org string) error {
	return e.q.DeleteFlagOverride(ctx, sqlc.DeleteFlagOverrideParams{FlagKey: key, OrgID: org})
}
