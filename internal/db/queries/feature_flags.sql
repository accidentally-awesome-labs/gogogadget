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

-- DeleteFeatureFlag removes the flag; flag_overrides cascade (FK ON DELETE
-- CASCADE, migration 0008).
-- name: DeleteFeatureFlag :exec
DELETE FROM feature_flags WHERE key = $1;

-- ListFlagOverridesByFlag joins orgs for display names.
-- name: ListFlagOverridesByFlag :many
SELECT o.clerk_org_id, o.name, f.enabled AS override_enabled
FROM flag_overrides f
JOIN orgs o ON o.clerk_org_id = f.clerk_org_id
WHERE f.flag_key = $1
ORDER BY o.name;

-- name: DeleteFlagOverride :exec
DELETE FROM flag_overrides WHERE flag_key = $1 AND clerk_org_id = $2;
