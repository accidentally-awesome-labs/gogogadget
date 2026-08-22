-- content_revisions (append-only snapshot per save; restoring an old row is itself a save)

-- name: InsertRevision :one
INSERT INTO content_revisions (entry_id, title, summary, body_md, meta, editor_id)
VALUES (
  sqlc.arg(entry_id), sqlc.arg(title), sqlc.arg(summary),
  sqlc.arg(body_md), sqlc.arg(meta), sqlc.arg(editor_id)
)
RETURNING *;

-- name: ListRevisionsByEntry :many
SELECT * FROM content_revisions
WHERE entry_id = sqlc.arg(entry_id)
ORDER BY created_at DESC
LIMIT sqlc.arg(lim);

-- A revision id belonging to another entry must MISS, never cross-load.
-- name: GetRevision :one
SELECT * FROM content_revisions WHERE id = sqlc.arg(id) AND entry_id = sqlc.arg(entry_id);
