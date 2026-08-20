-- +goose Up
-- Email digest: a periodic rollup of the notifications a user already
-- received in-app. Frequency lives on the user (not on a preferences row)
-- because it is a cadence, not a mute: 'off' is the opt-out, and the default
-- is 'weekly' so the feature is visible out of the box without being noisy.
--
-- last_digest_at is both the "when did we last send" stamp and the window
-- start for the next digest, so a missed run cannot silently skip content.
ALTER TABLE users
  ADD COLUMN digest_frequency TEXT NOT NULL DEFAULT 'weekly'
    CHECK (digest_frequency IN ('off', 'daily', 'weekly')),
  ADD COLUMN last_digest_at TIMESTAMPTZ;

-- The due-user scan reads only opted-in rows.
CREATE INDEX users_digest_due_idx ON users (last_digest_at)
  WHERE digest_frequency <> 'off';

-- +goose Down
DROP INDEX IF EXISTS users_digest_due_idx;
ALTER TABLE users
  DROP COLUMN IF EXISTS digest_frequency,
  DROP COLUMN IF EXISTS last_digest_at;
