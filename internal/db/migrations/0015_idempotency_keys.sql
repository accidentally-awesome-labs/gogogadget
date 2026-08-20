-- +goose Up
-- Idempotency keys for unsafe API calls: a client that retries after a
-- timeout must not create a second project. The key is scoped to the
-- organization (not the token) so a retry with a rotated token still
-- deduplicates, and stores the endpoint + a hash of the body so reusing one
-- key for a different request is a loud conflict rather than a wrong replay.
--
-- status = 0 means "claimed, still running": the row is inserted before the
-- handler runs, so two concurrent retries cannot both execute.
CREATE TABLE idempotency_keys (
  clerk_org_id TEXT NOT NULL REFERENCES orgs(clerk_org_id) ON DELETE CASCADE,
  key TEXT NOT NULL,
  endpoint TEXT NOT NULL,
  request_hash TEXT NOT NULL,
  status INT NOT NULL DEFAULT 0,
  -- The exact bytes the first request emitted. BYTEA, not JSONB: this is a
  -- stored HTTP response body, and JSONB would re-serialize it (key order,
  -- whitespace), so a replay would not be byte-identical to the original.
  response BYTEA,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (clerk_org_id, key)
);

-- The janitor sweeps by age.
CREATE INDEX idempotency_keys_created_idx ON idempotency_keys (created_at);

-- +goose Down
DROP TABLE IF EXISTS idempotency_keys;
