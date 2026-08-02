-- name: GetOrgByClerkID :one
SELECT * FROM orgs WHERE clerk_org_id = $1;

-- name: UpsertOrg :one
INSERT INTO orgs (clerk_org_id, name, slug, image_url)
VALUES ($1, $2, $3, $4)
ON CONFLICT (clerk_org_id) DO UPDATE
SET name = EXCLUDED.name, slug = EXCLUDED.slug, image_url = EXCLUDED.image_url, updated_at = now()
RETURNING *;

-- name: DeleteOrg :exec
DELETE FROM orgs WHERE clerk_org_id = $1;

-- name: GetOrgsForUser :many
SELECT o.* FROM orgs o
JOIN org_members m ON m.clerk_org_id = o.clerk_org_id
WHERE m.clerk_user_id = $1
ORDER BY o.name;

-- name: ListOrgsWithStats :many
SELECT o.*,
  (SELECT count(*) FROM org_members m WHERE m.clerk_org_id = o.clerk_org_id) AS member_count,
  COALESCE(s.product_key, 'free') AS product_key
FROM orgs o
LEFT JOIN subscriptions s ON s.clerk_org_id = o.clerk_org_id
ORDER BY o.created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountOrgs :one
SELECT count(*) FROM orgs;
