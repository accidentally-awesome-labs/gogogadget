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

-- name: SetUserAdminRoleByEmail :exec
-- ADMIN_EMAIL bootstrap: grants the full role on first sight of the address.
UPDATE users SET admin_role = $2, updated_at = now() WHERE email = $1;

-- name: SetUserAdminRole :exec
UPDATE users SET admin_role = $2, updated_at = now() WHERE clerk_user_id = $1;

-- name: CountFullAdmins :one
-- Lockout guard: demoting the last full admin would leave a platform whose
-- only remaining staff can read but never act — including never restoring
-- anyone's role.
SELECT count(*) FROM users WHERE admin_role = 'admin' AND disabled_at IS NULL;

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

-- name: ListUsersDueForDigest :many
-- Opted-in users whose last digest is older than their chosen cadence.
-- A user who has never received one is due immediately; the handler stamps
-- even when there is nothing to say, so a quiet account is not rescanned
-- every pass. Disabled accounts never receive mail.
SELECT * FROM users
WHERE disabled_at IS NULL
  AND digest_frequency <> 'off'
  AND (
    last_digest_at IS NULL
    OR last_digest_at < now() - (
      CASE digest_frequency WHEN 'daily' THEN interval '1 day' ELSE interval '7 days' END
    )
  )
ORDER BY last_digest_at NULLS FIRST
LIMIT $1;

-- name: SetUserDigestFrequency :exec
UPDATE users SET digest_frequency = $2, updated_at = now() WHERE clerk_user_id = $1;

-- name: MarkUserDigestSent :exec
-- Stamped after a successful send: the stamp is also the next window's start,
-- so writing it before delivery would silently drop that period's content.
UPDATE users SET last_digest_at = now(), updated_at = now() WHERE clerk_user_id = $1;

-- name: SetUserLocale :exec
-- '' restores "follow the browser" rather than pinning English.
UPDATE users SET locale = $2, updated_at = now() WHERE clerk_user_id = $1;

-- name: SetUserTheme :exec
UPDATE users SET theme = $2, updated_at = now() WHERE clerk_user_id = $1;
