-- +goose Up
CREATE TABLE usage_events (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  clerk_org_id TEXT NOT NULL REFERENCES orgs(clerk_org_id) ON DELETE CASCADE,
  name TEXT NOT NULL,                 -- e.g. 'ai_tokens'
  value BIGINT NOT NULL DEFAULT 1,
  metadata JSONB NOT NULL DEFAULT '{}',
  external_id TEXT NOT NULL,          -- caller-level dedup hint ("" = none)
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  flushed_at TIMESTAMPTZ
);
CREATE INDEX usage_events_flush_idx ON usage_events (id) WHERE flushed_at IS NULL;
CREATE INDEX usage_events_org_idx ON usage_events (clerk_org_id, name, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS usage_events;
