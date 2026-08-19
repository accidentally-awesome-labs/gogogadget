-- webhook_endpoints (customer-facing outbound webhook targets)

-- name: InsertWebhookEndpoint :one
INSERT INTO webhook_endpoints (clerk_org_id, created_by, url, secret, event_types, description)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListWebhookEndpointsByOrg :many
SELECT * FROM webhook_endpoints
WHERE clerk_org_id = $1
ORDER BY created_at DESC;

-- name: GetWebhookEndpoint :one
SELECT * FROM webhook_endpoints WHERE id = $1 AND clerk_org_id = $2;

-- name: SetWebhookEndpointDisabled :exec
UPDATE webhook_endpoints
SET disabled_at = CASE WHEN sqlc.arg(disabled)::bool THEN now() ELSE NULL END, updated_at = now()
WHERE id = sqlc.arg(id) AND clerk_org_id = sqlc.arg(clerk_org_id);

-- name: ListActiveEndpointsForEvent :many
-- '{}' event_types = subscribed to everything.
SELECT * FROM webhook_endpoints
WHERE clerk_org_id = $1
  AND disabled_at IS NULL
  AND (event_types = '{}' OR sqlc.arg(event_type)::text = ANY(event_types));

-- Rotation: the current secret becomes the previous one (still verifying
-- during the grace window), and a fresh secret takes over.
-- name: RotateWebhookEndpointSecret :one
UPDATE webhook_endpoints
SET secret_previous = secret, secret = sqlc.arg(secret), secret_rotated_at = now(), updated_at = now()
WHERE id = sqlc.arg(id) AND clerk_org_id = sqlc.arg(clerk_org_id)
RETURNING *;

-- Janitor: drop previous secrets whose grace window has closed.
-- name: ClearExpiredPreviousSecrets :execrows
UPDATE webhook_endpoints
SET secret_previous = '', secret_rotated_at = NULL, updated_at = now()
WHERE secret_previous <> '' AND secret_rotated_at < $1;
