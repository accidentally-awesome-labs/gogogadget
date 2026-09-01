-- +goose Up
CREATE TABLE webhook_outbox (
  id BIGSERIAL PRIMARY KEY,
  org_id TEXT NOT NULL,
  event_type TEXT NOT NULL,
  payload JSONB NOT NULL,
  attempts INT NOT NULL DEFAULT 0,
  available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX webhook_outbox_ready_idx ON webhook_outbox (available_at, created_at);
-- +goose Down
DROP TABLE webhook_outbox;
