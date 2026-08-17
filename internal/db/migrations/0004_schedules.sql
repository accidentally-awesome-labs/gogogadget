-- +goose Up
CREATE TABLE schedules (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  name TEXT NOT NULL,
  kind TEXT NOT NULL,                 -- jobs kind to enqueue
  payload JSONB NOT NULL DEFAULT '{}',
  clerk_org_id TEXT REFERENCES orgs(clerk_org_id) ON DELETE CASCADE,  -- NULL = system-wide
  every_seconds INT NOT NULL CHECK (every_seconds >= 60),
  next_run_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_run_at TIMESTAMPTZ,
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX schedules_due_idx ON schedules (next_run_at) WHERE enabled;

-- +goose Down
DROP TABLE IF EXISTS schedules;
