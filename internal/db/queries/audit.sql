-- name: InsertAuditLog :one
INSERT INTO audit_log (clerk_org_id, clerk_user_id, action, metadata)
VALUES ($1, $2, $3, $4)
RETURNING id;

-- name: ListAuditByOrg :many
SELECT a.*, COALESCE(u.email, '') AS actor_email
FROM audit_log a
LEFT JOIN users u ON u.clerk_user_id = a.clerk_user_id
WHERE a.clerk_org_id = $1
ORDER BY a.created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountAuditByOrg :one
SELECT count(*) FROM audit_log WHERE clerk_org_id = $1;

-- name: RecentAuditByOrg :many
SELECT a.*, COALESCE(u.email, '') AS actor_email
FROM audit_log a
LEFT JOIN users u ON u.clerk_user_id = a.clerk_user_id
WHERE a.clerk_org_id = $1
ORDER BY a.created_at DESC
LIMIT $2;
