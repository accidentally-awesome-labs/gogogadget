-- users mirror table (identity is Clerk; these rows are a local query cache)

-- name: GetUserByClerkID :one
SELECT * FROM users WHERE clerk_user_id = $1;

-- name: UpsertUser :one
INSERT INTO users (clerk_user_id, email, name, avatar_url)
VALUES ($1, $2, $3, $4)
ON CONFLICT (clerk_user_id) DO UPDATE
SET email = EXCLUDED.email, name = EXCLUDED.name, avatar_url = EXCLUDED.avatar_url, updated_at = now()
RETURNING *;

-- name: DeleteUser :exec
DELETE FROM users WHERE clerk_user_id = $1;

-- name: SetUserDisabled :exec
UPDATE users SET disabled_at = $2, updated_at = now() WHERE clerk_user_id = $1;

-- name: SetUserAdminByEmail :exec
UPDATE users SET is_admin = $2, updated_at = now() WHERE email = $1;

-- name: ListUsers :many
SELECT * FROM users
WHERE ($1::text = '' OR email ILIKE '%' || $1 || '%')
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountUsers :one
SELECT count(*) FROM users
WHERE ($1::text = '' OR email ILIKE '%' || $1 || '%');

-- name: CountUsersSince :one
SELECT count(*) FROM users WHERE created_at >= $1;
