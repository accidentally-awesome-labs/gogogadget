-- files (uploaded storage; org-scoped rows, bytes enforced at upload time)

-- name: InsertFile :one
INSERT INTO files (org_id, uploader_user_id, filename, content_type, size_bytes, storage_key)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListFilesByOrg :many
SELECT * FROM files
WHERE org_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountFilesByOrg :one
SELECT count(*) FROM files WHERE org_id = $1;

-- name: GetFileByID :one
SELECT * FROM files WHERE id = $1 AND org_id = $2;

-- name: DeleteFile :exec
DELETE FROM files WHERE id = $1 AND org_id = $2;

-- name: SumBytesByOrg :one
SELECT COALESCE(sum(size_bytes), 0)::bigint FROM files WHERE org_id = $1;
