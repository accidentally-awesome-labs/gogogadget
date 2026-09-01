-- name: UpsertMembership :exec
INSERT INTO org_members (org_id, user_id, role)
VALUES ($1, $2, $3)
ON CONFLICT (org_id, user_id) DO UPDATE
SET role = EXCLUDED.role;

-- name: DeleteMembership :exec
DELETE FROM org_members WHERE org_id = $1 AND user_id = $2;

-- name: ListMembersByOrg :many
SELECT u.user_id, u.email, u.name, u.avatar_url, m.role, m.created_at
FROM org_members m
JOIN users u ON u.user_id = m.user_id
WHERE m.org_id = $1
ORDER BY m.created_at;

-- name: CountMembersByOrg :one
SELECT count(*) FROM org_members WHERE org_id = $1;

-- name: GetMembership :one
SELECT * FROM org_members WHERE org_id = $1 AND user_id = $2;

-- Sole-admin guard for account deletion: a multi-member org whose only admin
-- leaves would be orphaned.
-- name: CountAdminsByOrg :one
SELECT count(*) FROM org_members WHERE org_id = $1 AND role = 'org:admin';
