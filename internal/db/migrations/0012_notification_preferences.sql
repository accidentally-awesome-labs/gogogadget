-- +goose Up
-- Per-user notification mutes. Absent row = on (default-on); a row with
-- in_app = false mutes that kind for that user.
CREATE TABLE notification_preferences (
  clerk_user_id TEXT NOT NULL REFERENCES users(clerk_user_id) ON DELETE CASCADE,
  kind          TEXT NOT NULL,
  in_app        BOOLEAN NOT NULL DEFAULT TRUE,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (clerk_user_id, kind)
);

-- +goose Down
DROP TABLE IF EXISTS notification_preferences;
