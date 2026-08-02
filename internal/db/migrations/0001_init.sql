-- +goose Up
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
  clerk_user_id  TEXT PRIMARY KEY,
  email          CITEXT NOT NULL UNIQUE,
  name           TEXT NOT NULL DEFAULT '',
  avatar_url     TEXT NOT NULL DEFAULT '',
  is_admin       BOOLEAN NOT NULL DEFAULT FALSE,
  disabled_at    TIMESTAMPTZ,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE orgs (
  clerk_org_id   TEXT PRIMARY KEY,
  name           TEXT NOT NULL,
  slug           TEXT NOT NULL UNIQUE,
  image_url      TEXT NOT NULL DEFAULT '',
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE org_members (
  clerk_org_id   TEXT NOT NULL REFERENCES orgs(clerk_org_id) ON DELETE CASCADE,
  clerk_user_id  TEXT NOT NULL REFERENCES users(clerk_user_id) ON DELETE CASCADE,
  -- Raw Clerk role slug ('org:admin', 'org:member', or buyer-added custom roles).
  -- No CHECK constraint: a custom role must never wedge membership webhooks.
  role           TEXT NOT NULL,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (clerk_org_id, clerk_user_id)
);
-- Feeds the SelectOrg page (orgs-for-user lookup).
CREATE INDEX org_members_user_idx ON org_members (clerk_user_id);

-- Idempotency for BOTH webhook providers.
CREATE TABLE webhook_events (
  id           TEXT PRIMARY KEY,       -- the webhook-id / svix-id header value
  provider     TEXT NOT NULL CHECK (provider IN ('clerk','polar')),
  event_type   TEXT NOT NULL,
  processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE subscriptions (
  id                    BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  clerk_org_id          TEXT NOT NULL UNIQUE REFERENCES orgs(clerk_org_id) ON DELETE CASCADE,
  polar_subscription_id TEXT UNIQUE,
  polar_customer_id     TEXT NOT NULL,
  product_key           TEXT NOT NULL,             -- 'free'|'pro'|'team'
  status                TEXT NOT NULL CHECK (status IN
    ('incomplete','incomplete_expired','trialing','active','past_due','canceled','unpaid')),
    -- Mirrors Polar's exact status enum; 'revoked' is a webhook EVENT, never a stored status.
  current_period_end    TIMESTAMPTZ,
  cancel_at_period_end  BOOLEAN NOT NULL DEFAULT FALSE,
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- No FKs: audit rows survive org/user deletion.
CREATE TABLE audit_log (
  id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  clerk_org_id  TEXT,
  clerk_user_id TEXT,
  action        TEXT NOT NULL,
  metadata      JSONB NOT NULL DEFAULT '{}',
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_org_idx ON audit_log (clerk_org_id, created_at DESC);

-- Canonical CRUD example resource.
CREATE TABLE projects (
  id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  clerk_org_id TEXT NOT NULL REFERENCES orgs(clerk_org_id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  status       TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','archived')),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX projects_org_idx ON projects (clerk_org_id, created_at DESC);

CREATE TABLE jobs (
  id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  kind         TEXT NOT NULL,
  payload      JSONB NOT NULL,
  run_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  attempts     INT NOT NULL DEFAULT 0,
  max_attempts INT NOT NULL DEFAULT 8,
  last_error   TEXT,
  done_at      TIMESTAMPTZ,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX jobs_pending_idx ON jobs (run_at) WHERE done_at IS NULL;

-- Bearer tokens for /api/v1. Only SHA-256 hashes are stored.
CREATE TABLE api_tokens (
  id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  clerk_org_id TEXT NOT NULL REFERENCES orgs(clerk_org_id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  token_hash   TEXT NOT NULL UNIQUE,
  scope        TEXT NOT NULL CHECK (scope IN ('read','write')),
  last_used_at TIMESTAMPTZ,
  expires_at   TIMESTAMPTZ,
  revoked_at   TIMESTAMPTZ,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS api_tokens;
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS subscriptions;
DROP TABLE IF EXISTS webhook_events;
DROP TABLE IF EXISTS org_members;
DROP TABLE IF EXISTS orgs;
DROP TABLE IF EXISTS users;
DROP EXTENSION IF EXISTS citext;
