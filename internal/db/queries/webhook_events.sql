-- Idempotency for BOTH webhook providers: pgx.ErrNoRows on :one means the
-- event was already processed → stop, return 200.
-- name: InsertWebhookEvent :one
INSERT INTO webhook_events (id, provider, event_type)
VALUES ($1, $2, $3)
ON CONFLICT (id) DO NOTHING
RETURNING id;

-- name: DeleteOldWebhookEvents :exec
DELETE FROM webhook_events WHERE processed_at < now() - interval '30 days';
