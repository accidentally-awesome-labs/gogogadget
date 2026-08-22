-- content_media (platform-scoped images for content bodies; served inline, never org-scoped)

-- name: InsertMedia :one
INSERT INTO content_media (filename, content_type, size_bytes, storage_key, alt, uploaded_by)
VALUES (
  sqlc.arg(filename), sqlc.arg(content_type), sqlc.arg(size_bytes),
  sqlc.arg(storage_key), sqlc.arg(alt), sqlc.arg(uploaded_by)
)
RETURNING *;

-- name: ListMedia :many
SELECT * FROM content_media ORDER BY created_at DESC LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: CountMedia :one
SELECT count(*) FROM content_media;

-- name: GetMedia :one
SELECT * FROM content_media WHERE id = $1;

-- name: DeleteMedia :exec
DELETE FROM content_media WHERE id = $1;
