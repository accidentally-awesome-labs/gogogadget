-- Queries for the users mirror table. (Full set lands with the identity step.)

-- name: GetUserByClerkID :one
SELECT * FROM users WHERE clerk_user_id = $1;
