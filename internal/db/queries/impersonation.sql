-- impersonation_sessions (admin "view as" sessions, opaque cookie ids)

-- name: InsertImpersonationSession :one
INSERT INTO impersonation_sessions (id, admin_user_id, target_user_id, target_org_id, expires_at, reason)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetImpersonationSession :one
SELECT * FROM impersonation_sessions WHERE id = $1;

-- name: EndImpersonationSession :exec
UPDATE impersonation_sessions SET ended_at = now() WHERE id = $1;

-- Account deletion: both FK columns reference users(user_id) with NO
-- cascade, so the rows must go before the user row does.
-- name: DeleteImpersonationSessionsForUser :exec
DELETE FROM impersonation_sessions WHERE target_user_id = $1 OR admin_user_id = $1;
