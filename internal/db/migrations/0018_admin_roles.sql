-- +goose Up
-- Staff access becomes a role instead of a boolean.
--
-- is_admin was all-or-nothing: the only way to let a support person READ the
-- admin dashboard was to also grant impersonation, user disable, feature-flag
-- mutation, schedule run-now, and dead-letter requeue. 'support' is that
-- missing read-only tier.
--
-- One column with three states rather than a second boolean: is_admin +
-- is_support would allow (true, true) and (false, true), and nothing in the
-- code would say which wins.
ALTER TABLE users ADD COLUMN admin_role TEXT NOT NULL DEFAULT ''
  CHECK (admin_role IN ('', 'support', 'admin'));

UPDATE users SET admin_role = 'admin' WHERE is_admin;

ALTER TABLE users DROP COLUMN is_admin;

-- +goose Down
ALTER TABLE users ADD COLUMN is_admin BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE users SET is_admin = TRUE WHERE admin_role = 'admin';
ALTER TABLE users DROP COLUMN admin_role;
