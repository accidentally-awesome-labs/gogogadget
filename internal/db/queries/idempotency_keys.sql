-- name: ClaimIdempotencyKey :one
-- Atomic claim: the PK conflict is the lock. Returns no rows when the key is
-- already taken — the caller then reads the existing row and decides between
-- replay, conflict, and "still in flight".
INSERT INTO idempotency_keys (clerk_org_id, key, endpoint, request_hash)
VALUES ($1, $2, $3, $4)
ON CONFLICT (clerk_org_id, key) DO NOTHING
RETURNING *;

-- name: GetIdempotencyKey :one
SELECT * FROM idempotency_keys WHERE clerk_org_id = $1 AND key = $2;

-- name: CompleteIdempotencyKey :exec
-- Store the outcome so a retry replays it verbatim.
UPDATE idempotency_keys
SET status = $3, response = $4, updated_at = now()
WHERE clerk_org_id = $1 AND key = $2;

-- name: ReleaseIdempotencyKey :exec
-- Drop the claim so the client can genuinely retry. Used when the handler
-- fails in a way that says nothing about whether the work should happen
-- (5xx): pinning that outcome to the key would poison it permanently.
DELETE FROM idempotency_keys WHERE clerk_org_id = $1 AND key = $2;

-- name: DeleteOldIdempotencyKeys :execrows
-- Retention: keys outlive their usefulness once no client will retry.
DELETE FROM idempotency_keys WHERE created_at < $1;
