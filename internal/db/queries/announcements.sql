-- announcements (platform-wide banner; at most one active — partial unique index)

-- name: ListAnnouncements :many
SELECT * FROM announcements ORDER BY created_at DESC;

-- name: GetActiveAnnouncement :one
SELECT * FROM announcements WHERE active;

-- name: CreateAnnouncement :one
INSERT INTO announcements (kind, message, url)
VALUES ($1, $2, $3)
RETURNING *;

-- name: DeactivateAnnouncements :exec
UPDATE announcements SET active = false, updated_at = now() WHERE active;

-- name: SetAnnouncementActive :exec
UPDATE announcements SET active = $2, updated_at = now() WHERE id = $1;

-- name: DeleteAnnouncement :exec
DELETE FROM announcements WHERE id = $1;
