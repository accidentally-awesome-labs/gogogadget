-- name: InsertAPIToken :one
INSERT INTO api_tokens (clerk_org_id, name, token_hash, scope, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING id;

-- name: GetAPITokenByHash :one
SELECT * FROM api_tokens WHERE token_hash = $1 AND revoked_at IS NULL;

-- name: TouchAPIToken :exec
UPDATE api_tokens SET last_used_at = now() WHERE id = $1;

-- name: ListAPITokensByOrg :many
SELECT * FROM api_tokens
WHERE clerk_org_id = $1
ORDER BY created_at DESC;

-- name: RevokeAPIToken :exec
UPDATE api_tokens SET revoked_at = now() WHERE id = $1 AND clerk_org_id = $2;
