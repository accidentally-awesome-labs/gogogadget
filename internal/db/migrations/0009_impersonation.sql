-- +goose Up
CREATE TABLE impersonation_sessions (
  id TEXT PRIMARY KEY,                -- 32B hex random
  admin_user_id TEXT NOT NULL REFERENCES users(clerk_user_id),
  target_user_id TEXT NOT NULL REFERENCES users(clerk_user_id),
  target_org_id TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL,
  ended_at TIMESTAMPTZ
);
CREATE INDEX impersonation_admin_idx ON impersonation_sessions (admin_user_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS impersonation_sessions;
