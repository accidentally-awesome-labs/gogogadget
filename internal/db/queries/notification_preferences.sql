-- notification_preferences (per-user per-kind mutes; absent row = default-on)

-- name: GetNotificationPreference :one
SELECT * FROM notification_preferences
WHERE user_id = $1 AND kind = $2;

-- name: ListNotificationPreferencesByUser :many
SELECT * FROM notification_preferences
WHERE user_id = $1
ORDER BY kind;

-- name: UpsertNotificationPreference :exec
INSERT INTO notification_preferences (user_id, kind, in_app)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, kind) DO UPDATE
SET in_app = EXCLUDED.in_app, updated_at = now();
