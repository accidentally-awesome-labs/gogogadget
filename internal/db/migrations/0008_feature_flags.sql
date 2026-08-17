-- +goose Up
CREATE TABLE feature_flags (
  key TEXT PRIMARY KEY,
  description TEXT NOT NULL DEFAULT '',
  enabled BOOLEAN NOT NULL DEFAULT FALSE,
  rollout INT NOT NULL DEFAULT 100 CHECK (rollout BETWEEN 0 AND 100),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE flag_overrides (
  flag_key TEXT NOT NULL REFERENCES feature_flags(key) ON DELETE CASCADE,
  clerk_org_id TEXT NOT NULL REFERENCES orgs(clerk_org_id) ON DELETE CASCADE,
  enabled BOOLEAN NOT NULL,
  PRIMARY KEY (flag_key, clerk_org_id)
);

-- +goose Down
DROP TABLE IF EXISTS flag_overrides;
DROP TABLE IF EXISTS feature_flags;
