-- feature_flags + flag_overrides (admin-managed toggles with per-org overrides)

-- name: ListFeatureFlags :many
SELECT * FROM feature_flags ORDER BY key;

-- name: GetFeatureFlag :one
SELECT * FROM feature_flags WHERE key = $1;

-- name: SetFeatureFlagEnabled :exec
UPDATE feature_flags SET enabled = $2, updated_at = now() WHERE key = $1;

-- name: SetFeatureFlagRollout :exec
UPDATE feature_flags SET rollout = $2, updated_at = now() WHERE key = $1;

-- name: UpsertFeatureFlag :exec
INSERT INTO feature_flags (key, description, enabled, rollout)
VALUES ($1, $2, $3, $4)
ON CONFLICT (key) DO NOTHING;

-- name: UpsertFlagOverride :exec
INSERT INTO flag_overrides (flag_key, clerk_org_id, enabled)
VALUES ($1, $2, $3)
ON CONFLICT (flag_key, clerk_org_id) DO UPDATE SET enabled = EXCLUDED.enabled;

-- name: GetFlagOverride :one
SELECT * FROM flag_overrides WHERE flag_key = $1 AND clerk_org_id = $2;
