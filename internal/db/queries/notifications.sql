-- notifications (per-user in-app rows; unread badge + SSE stream)

-- name: InsertNotification :one
INSERT INTO notifications (clerk_org_id, clerk_user_id, kind, title, body, url)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListNotificationsByUser :many
SELECT * FROM notifications
WHERE clerk_org_id = $1 AND clerk_user_id = $2
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: CountNotificationsByUser :one
SELECT count(*) FROM notifications
WHERE clerk_org_id = $1 AND clerk_user_id = $2;

-- name: CountUnreadByUser :one
SELECT count(*) FROM notifications
WHERE clerk_org_id = $1 AND clerk_user_id = $2 AND read_at IS NULL;

-- name: MarkNotificationRead :exec
UPDATE notifications SET read_at = now()
WHERE id = $1 AND clerk_org_id = $2 AND clerk_user_id = $3;

-- name: MarkAllRead :exec
UPDATE notifications SET read_at = now()
WHERE clerk_org_id = $1 AND clerk_user_id = $2 AND read_at IS NULL;

-- name: DeleteOldReadNotifications :exec
DELETE FROM notifications WHERE read_at IS NOT NULL AND read_at < now() - interval '90 days';

-- name: ListNotificationsSince :many
-- Digest content: what the user was notified about during the window. Read
-- rows are included on purpose — a digest is a summary of the period, not an
-- inbox of unread items.
SELECT * FROM notifications
WHERE clerk_user_id = $1 AND created_at > $2
ORDER BY created_at DESC
LIMIT $3;
