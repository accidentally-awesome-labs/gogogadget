-- name: GetOrgByID :one
SELECT * FROM orgs WHERE org_id = $1;

-- name: GetOrgBySlug :one
SELECT * FROM orgs WHERE slug = $1;

-- name: UpsertOrg :one
INSERT INTO orgs (org_id, name, slug, image_url)
VALUES ($1, $2, $3, $4)
ON CONFLICT (org_id) DO UPDATE
SET name = EXCLUDED.name, slug = EXCLUDED.slug, image_url = EXCLUDED.image_url, updated_at = now()
RETURNING *;

-- name: DeleteOrg :exec
DELETE FROM orgs WHERE org_id = $1;

-- name: GetOrgsForUser :many
SELECT o.* FROM orgs o
JOIN org_members m ON m.org_id = o.org_id
WHERE m.user_id = $1
ORDER BY o.name;

-- name: ListOrgsWithStats :many
-- Plan badge mirrors billing.Entitled: paid key only while the subscription
-- confers access (active/trialing/past_due, or canceled before period end).
SELECT o.*,
  (SELECT count(*) FROM org_members m WHERE m.org_id = o.org_id) AS member_count,
  CASE
    WHEN s.status IN ('active', 'trialing', 'past_due') THEN s.product_key
    WHEN s.status = 'canceled' AND s.current_period_end > now() THEN s.product_key
    ELSE 'free'
  END AS product_key
FROM orgs o
LEFT JOIN subscriptions s ON s.org_id = o.org_id
ORDER BY o.created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountOrgs :one
SELECT count(*) FROM orgs;

-- ListOrgs feeds admin org pickers (no stats baggage).
-- name: ListOrgs :many
SELECT * FROM orgs ORDER BY name;
