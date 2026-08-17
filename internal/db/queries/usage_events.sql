-- usage_events (metered usage; flushed to Polar by the usage.flush schedule)

-- name: InsertUsageEvent :one
INSERT INTO usage_events (clerk_org_id, name, value, metadata, external_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: SumUsageByNameSince :one
SELECT COALESCE(sum(value), 0)::bigint FROM usage_events
WHERE clerk_org_id = $1 AND name = $2 AND created_at >= $3;

-- name: ClaimUsageBatch :many
-- The 60s grace window avoids racing in-flight Record calls.
UPDATE usage_events SET flushed_at = now()
WHERE id IN (
  SELECT id FROM usage_events
  WHERE flushed_at IS NULL AND created_at < now() - interval '60 seconds'
  ORDER BY id
  LIMIT 100
  FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: UnflushUsageBatch :exec
-- The flush FAILED (Polar down etc.) — return the batch to the pool so the
-- next tick retries (at-least-once; Polar dedups on external_id).
UPDATE usage_events SET flushed_at = NULL WHERE id = ANY($1::bigint[]);
