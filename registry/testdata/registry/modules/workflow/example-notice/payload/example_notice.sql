-- +goose Up
CREATE TABLE example_notices (
  id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name       TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX example_notices_created_idx ON example_notices (created_at DESC);

-- +goose Down
-- forward-only migration
