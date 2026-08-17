-- impersonation_sessions (admin "view as" sessions, opaque cookie ids)

-- name: InsertImpersonationSession :one
INSERT INTO impersonation_sessions (id, admin_user_id, target_user_id, target_org_id, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetImpersonationSession :one
SELECT * FROM impersonation_sessions WHERE id = $1;

-- name: EndImpersonationSession :exec
UPDATE impersonation_sessions SET ended_at = now() WHERE id = $1;
