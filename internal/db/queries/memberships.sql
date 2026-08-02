-- name: UpsertMembership :exec
INSERT INTO org_members (clerk_org_id, clerk_user_id, role)
VALUES ($1, $2, $3)
ON CONFLICT (clerk_org_id, clerk_user_id) DO UPDATE
SET role = EXCLUDED.role;

-- name: DeleteMembership :exec
DELETE FROM org_members WHERE clerk_org_id = $1 AND clerk_user_id = $2;

-- name: ListMembersByOrg :many
SELECT u.clerk_user_id, u.email, u.name, u.avatar_url, m.role, m.created_at
FROM org_members m
JOIN users u ON u.clerk_user_id = m.clerk_user_id
WHERE m.clerk_org_id = $1
ORDER BY m.created_at;

-- name: CountMembersByOrg :one
SELECT count(*) FROM org_members WHERE clerk_org_id = $1;

-- name: GetMembership :one
SELECT * FROM org_members WHERE clerk_org_id = $1 AND clerk_user_id = $2;
