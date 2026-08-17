-- +goose Up
CREATE TABLE webhook_endpoints (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  clerk_org_id TEXT NOT NULL REFERENCES orgs(clerk_org_id) ON DELETE CASCADE,
  created_by TEXT NOT NULL,
  url TEXT NOT NULL,
  secret TEXT NOT NULL,               -- "whsec_" + base64url; stored for signing; shown once at creation
  event_types TEXT[] NOT NULL DEFAULT '{}',  -- '{}' = all events
  description TEXT NOT NULL DEFAULT '',
  disabled_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX webhook_endpoints_org_idx ON webhook_endpoints (clerk_org_id);

CREATE TABLE webhook_deliveries (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  endpoint_id BIGINT NOT NULL REFERENCES webhook_endpoints(id) ON DELETE CASCADE,
  clerk_org_id TEXT NOT NULL REFERENCES orgs(clerk_org_id) ON DELETE CASCADE,
  event_type TEXT NOT NULL,
  payload JSONB NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','success','dead')),
  attempts INT NOT NULL DEFAULT 0,
  last_response_status INT,
  last_error TEXT NOT NULL DEFAULT '',
  delivered_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX webhook_deliveries_endpoint_idx ON webhook_deliveries (endpoint_id, created_at DESC);
CREATE INDEX webhook_deliveries_org_idx ON webhook_deliveries (clerk_org_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS webhook_deliveries;
DROP TABLE IF EXISTS webhook_endpoints;
