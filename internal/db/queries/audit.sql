-- name: InsertAuditLog :one
INSERT INTO audit_log (org_id, user_id, action, metadata)
VALUES ($1, $2, $3, $4)
RETURNING id;

-- name: ListAuditByOrg :many
SELECT a.*, COALESCE(u.email, '') AS actor_email
FROM audit_log a
LEFT JOIN users u ON u.user_id = a.user_id
WHERE a.org_id = $1
ORDER BY a.created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountAuditByOrg :one
SELECT count(*) FROM audit_log WHERE org_id = $1;

-- name: RecentAuditByOrg :many
SELECT a.*, COALESCE(u.email, '') AS actor_email
FROM audit_log a
LEFT JOIN users u ON u.user_id = a.user_id
WHERE a.org_id = $1
ORDER BY a.created_at DESC
LIMIT $2;

-- Platform-wide audit viewer (admin). Empty filter matches everything.
-- name: ListAuditAll :many
SELECT a.*, COALESCE(u.email, '') AS actor_email
FROM audit_log a
LEFT JOIN users u ON u.user_id = a.user_id
WHERE (sqlc.arg(filter)::text = '' OR a.action ILIKE '%' || sqlc.arg(filter) || '%' OR a.org_id ILIKE '%' || sqlc.arg(filter) || '%')
ORDER BY a.created_at DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: CountAuditAll :one
SELECT count(*) FROM audit_log a
WHERE (sqlc.arg(filter)::text = '' OR a.action ILIKE '%' || sqlc.arg(filter) || '%' OR a.org_id ILIKE '%' || sqlc.arg(filter) || '%');

-- GDPR export: everything one user ever did, across orgs.
-- name: ListAuditByUser :many
SELECT a.*, COALESCE(u.email, '') AS actor_email
FROM audit_log a
LEFT JOIN users u ON u.user_id = a.user_id
WHERE a.user_id = sqlc.arg(user_id)
ORDER BY a.created_at DESC
LIMIT sqlc.arg(lim);

-- Retention janitor: AUDIT_RETENTION_DAYS > 0 deletes older rows. Returns
-- the count for logging. 0 = retain forever (the documented default).
-- name: DeleteOldAuditRows :execrows
DELETE FROM audit_log WHERE created_at < $1;
