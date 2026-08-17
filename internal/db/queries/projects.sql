-- name: ListProjectsByOrg :many
-- Postgres FTS (websearch syntax: quotes, OR, -negation) with an ILIKE
-- fallback so partial/short tokens still match; ranked when the FTS query
-- scores, else newest first.
SELECT * FROM projects
WHERE clerk_org_id = $1 AND status = 'active'
  AND ($2::text = '' OR search_tsv @@ websearch_to_tsquery('simple', $2) OR name ILIKE '%' || $2 || '%')
ORDER BY CASE WHEN $2::text = '' THEN 0 ELSE COALESCE(ts_rank(search_tsv, websearch_to_tsquery('simple', $2)), 0) END DESC,
         created_at DESC
LIMIT $3 OFFSET $4;

-- name: CountProjectsByOrgSearch :one
SELECT count(*) FROM projects
WHERE clerk_org_id = $1 AND status = 'active'
  AND ($2::text = '' OR search_tsv @@ websearch_to_tsquery('simple', $2) OR name ILIKE '%' || $2 || '%');

-- name: ListAllProjectsByOrg :many
-- Full list for CSV export (no pagination).
SELECT * FROM projects
WHERE clerk_org_id = $1 AND status = 'active'
ORDER BY created_at DESC;

-- name: CountProjectsByOrg :one
SELECT count(*) FROM projects WHERE clerk_org_id = $1 AND status = 'active';

-- name: GetProjectByID :one
SELECT * FROM projects WHERE id = $1 AND clerk_org_id = $2;

-- name: CreateProject :one
INSERT INTO projects (clerk_org_id, name)
VALUES ($1, $2)
RETURNING *;

-- name: UpdateProject :one
UPDATE projects SET name = $3, updated_at = now()
WHERE id = $1 AND clerk_org_id = $2
RETURNING *;

-- name: ArchiveProject :exec
UPDATE projects SET status = 'archived', updated_at = now()
WHERE id = $1 AND clerk_org_id = $2;

-- name: DeleteProject :exec
DELETE FROM projects WHERE id = $1 AND clerk_org_id = $2;
