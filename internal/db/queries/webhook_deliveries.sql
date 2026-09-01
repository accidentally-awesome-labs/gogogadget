-- webhook_deliveries (per-attempt audit + retry state)

-- name: InsertWebhookDelivery :one
INSERT INTO webhook_deliveries (endpoint_id, org_id, event_type, payload)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListDeliveriesByOrg :many
SELECT d.*, e.url AS endpoint_url FROM webhook_deliveries d
JOIN webhook_endpoints e ON e.id = d.endpoint_id
WHERE d.org_id = $1
ORDER BY d.created_at DESC
LIMIT 50;

-- name: GetWebhookDelivery :one
SELECT * FROM webhook_deliveries WHERE id = $1;

-- name: RecordDeliveryAttempt :exec
UPDATE webhook_deliveries
SET attempts = attempts + 1, last_response_status = $2, last_error = $3
WHERE id = $1;

-- name: MarkDeliverySuccess :exec
UPDATE webhook_deliveries
SET status = 'success', attempts = attempts + 1, last_response_status = $2,
    last_error = '', delivered_at = now()
WHERE id = $1;

-- name: MarkDeliveryDead :exec
UPDATE webhook_deliveries SET status = 'dead', last_error = $2 WHERE id = $1;

-- name: ResetWebhookDelivery :exec
UPDATE webhook_deliveries
SET status = 'pending', attempts = 0, last_response_status = NULL,
    last_error = '', delivered_at = NULL
WHERE id = $1 AND org_id = $2;
