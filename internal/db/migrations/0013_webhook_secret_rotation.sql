-- +goose Up
-- Secret rotation with a grace window: the previous secret keeps verifying
-- while receivers roll over. Deliveries sign with BOTH secrets during the
-- window (standard-webhooks allows a space-delimited signature list), and
-- the janitor clears the previous secret once the window closes.
ALTER TABLE webhook_endpoints
  ADD COLUMN secret_previous   TEXT NOT NULL DEFAULT '',
  ADD COLUMN secret_rotated_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE webhook_endpoints
  DROP COLUMN IF EXISTS secret_previous,
  DROP COLUMN IF EXISTS secret_rotated_at;
