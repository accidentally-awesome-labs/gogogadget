-- +goose Up
CREATE TABLE audit_export_outbox (
  id BIGSERIAL PRIMARY KEY,
  entry JSONB NOT NULL,
  attempts INT NOT NULL DEFAULT 0,
  available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_export_outbox_ready_idx ON audit_export_outbox (available_at, created_at);
-- +goose Down
DROP TABLE audit_export_outbox;
