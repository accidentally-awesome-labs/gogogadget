-- +goose Up
CREATE TABLE example_resources (
  id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  org_id     TEXT NOT NULL REFERENCES orgs(org_id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX example_resources_tenant_idx ON example_resources (org_id, created_at DESC);

-- +goose Down
-- forward-only migration
