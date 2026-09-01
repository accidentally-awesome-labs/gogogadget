-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE n integer;
BEGIN
  IF to_regclass('public.identity_subjects') IS NOT NULL OR to_regclass('public.identity_organizations') IS NOT NULL OR to_regclass('public.billing_accounts') IS NOT NULL THEN
    RAISE EXCEPTION 'provider_neutral_ids_generic_replacements_present';
  END IF;
  SELECT count(*) INTO n FROM information_schema.columns WHERE table_schema='public' AND ((table_name,column_name) IN (
    ('users','clerk_user_id'),('orgs','clerk_org_id'),('org_members','clerk_org_id'),('org_members','clerk_user_id'),
    ('subscriptions','clerk_org_id'),('subscriptions','polar_subscription_id'),('subscriptions','polar_customer_id'),
    ('audit_log','clerk_org_id'),('audit_log','clerk_user_id'),('projects','clerk_org_id'),('api_tokens','clerk_org_id'),
    ('files','clerk_org_id'),('schedules','clerk_org_id'),('notifications','clerk_org_id'),('notifications','clerk_user_id'),
    ('webhook_endpoints','clerk_org_id'),('webhook_deliveries','clerk_org_id'),('usage_events','clerk_org_id'),
    ('flag_overrides','clerk_org_id'),('impersonation_sessions','admin_user_id'),('impersonation_sessions','target_user_id'),
    ('notification_preferences','clerk_user_id'),('idempotency_keys','clerk_org_id')));
  IF n <> 23 THEN RAISE EXCEPTION 'provider_neutral_ids_legacy_columns_mismatch: expected 23, got %', n; END IF;
  IF EXISTS (SELECT 1 FROM (VALUES ('org_members_user_idx'),('audit_log_org_idx'),('projects_org_idx'),('files_org_idx'),('notifications_unread_idx'),('webhook_endpoints_org_idx'),('webhook_deliveries_org_idx'),('usage_events_org_idx')) AS expected(name) WHERE to_regclass('public.'||expected.name) IS NULL) THEN
    RAISE EXCEPTION 'provider_neutral_ids_legacy_indexes_mismatch';
  END IF;
  IF EXISTS (
    SELECT 1 FROM (VALUES
      ('org_members','org_members_clerk_org_id_fkey'),('org_members','org_members_clerk_user_id_fkey'),
      ('subscriptions','subscriptions_clerk_org_id_fkey'),('projects','projects_clerk_org_id_fkey'),
      ('api_tokens','api_tokens_clerk_org_id_fkey'),('files','files_clerk_org_id_fkey'),
      ('schedules','schedules_clerk_org_id_fkey'),('notifications','notifications_clerk_org_id_fkey'),
      ('notifications','notifications_clerk_user_id_fkey'),('webhook_endpoints','webhook_endpoints_clerk_org_id_fkey'),
      ('webhook_deliveries','webhook_deliveries_endpoint_id_fkey'),('webhook_deliveries','webhook_deliveries_clerk_org_id_fkey'),
      ('usage_events','usage_events_clerk_org_id_fkey'),('flag_overrides','flag_overrides_flag_key_fkey'),
      ('flag_overrides','flag_overrides_clerk_org_id_fkey'),('notification_preferences','notification_preferences_clerk_user_id_fkey'),
      ('idempotency_keys','idempotency_keys_clerk_org_id_fkey')
    ) AS expected(table_name,constraint_name)
    LEFT JOIN pg_constraint c ON c.conrelid=to_regclass('public.'||expected.table_name)
      AND c.conname=expected.constraint_name AND c.contype='f' AND c.confdeltype='c'
    WHERE c.oid IS NULL
  ) THEN
    RAISE EXCEPTION 'provider_neutral_ids_legacy_foreign_keys_mismatch';
  END IF;
  IF EXISTS (SELECT 1 FROM subscriptions WHERE polar_customer_id <> '' GROUP BY clerk_org_id HAVING count(DISTINCT polar_customer_id)>1) THEN RAISE EXCEPTION 'provider_neutral_ids_multiple_polar_customers_per_org'; END IF;
  IF EXISTS (SELECT 1 FROM subscriptions WHERE polar_customer_id <> '' GROUP BY polar_customer_id HAVING count(DISTINCT clerk_org_id)>1) THEN RAISE EXCEPTION 'provider_neutral_ids_shared_polar_customer'; END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE users RENAME COLUMN clerk_user_id TO user_id;
ALTER TABLE orgs RENAME COLUMN clerk_org_id TO org_id;
ALTER TABLE org_members RENAME COLUMN clerk_org_id TO org_id;
ALTER TABLE org_members RENAME COLUMN clerk_user_id TO user_id;
ALTER TABLE subscriptions RENAME COLUMN clerk_org_id TO org_id;
ALTER TABLE subscriptions RENAME COLUMN polar_subscription_id TO provider_subscription_id;
ALTER TABLE subscriptions RENAME COLUMN polar_customer_id TO provider_customer_id;
ALTER TABLE audit_log RENAME COLUMN clerk_org_id TO org_id;
ALTER TABLE audit_log RENAME COLUMN clerk_user_id TO user_id;
ALTER TABLE projects RENAME COLUMN clerk_org_id TO org_id;
ALTER TABLE api_tokens RENAME COLUMN clerk_org_id TO org_id;
ALTER TABLE files RENAME COLUMN clerk_org_id TO org_id;
ALTER TABLE schedules RENAME COLUMN clerk_org_id TO org_id;
ALTER TABLE notifications RENAME COLUMN clerk_org_id TO org_id;
ALTER TABLE notifications RENAME COLUMN clerk_user_id TO user_id;
ALTER TABLE webhook_endpoints RENAME COLUMN clerk_org_id TO org_id;
ALTER TABLE webhook_deliveries RENAME COLUMN clerk_org_id TO org_id;
ALTER TABLE usage_events RENAME COLUMN clerk_org_id TO org_id;
ALTER TABLE flag_overrides RENAME COLUMN clerk_org_id TO org_id;
ALTER TABLE notification_preferences RENAME COLUMN clerk_user_id TO user_id;
ALTER TABLE idempotency_keys RENAME COLUMN clerk_org_id TO org_id;
ALTER TABLE webhook_events DROP CONSTRAINT webhook_events_provider_check;
ALTER TABLE webhook_events ADD CONSTRAINT webhook_events_provider_check CHECK (provider IN ('clerk','polar','local'));
ALTER TABLE subscriptions ADD COLUMN provider TEXT DEFAULT 'polar';
UPDATE subscriptions SET provider='polar' WHERE provider IS NULL;
ALTER TABLE subscriptions ALTER COLUMN provider SET NOT NULL;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_provider_not_empty CHECK (provider<>'');

CREATE TABLE identity_subjects (
 provider TEXT NOT NULL, subject TEXT NOT NULL, user_id TEXT NOT NULL REFERENCES users(user_id) ON DELETE CASCADE,
 PRIMARY KEY(provider,subject), CONSTRAINT identity_subjects_provider_user_key UNIQUE(provider,user_id)
);
CREATE INDEX identity_subjects_user_idx ON identity_subjects(user_id);
CREATE TABLE identity_organizations (
 provider TEXT NOT NULL, subject TEXT NOT NULL, org_id TEXT NOT NULL REFERENCES orgs(org_id) ON DELETE CASCADE,
 PRIMARY KEY(provider,subject), CONSTRAINT identity_organizations_provider_org_key UNIQUE(provider,org_id)
);
CREATE INDEX identity_organizations_org_idx ON identity_organizations(org_id);
CREATE TABLE billing_accounts (
 provider TEXT NOT NULL, provider_customer_id TEXT NOT NULL, org_id TEXT NOT NULL REFERENCES orgs(org_id) ON DELETE CASCADE,
 PRIMARY KEY(provider,provider_customer_id), CONSTRAINT billing_accounts_provider_org_key UNIQUE(provider,org_id)
);
INSERT INTO identity_subjects(provider,subject,user_id) SELECT 'clerk',user_id,user_id FROM users;
INSERT INTO identity_organizations(provider,subject,org_id) SELECT 'clerk',org_id,org_id FROM orgs;
INSERT INTO billing_accounts(provider,provider_customer_id,org_id) SELECT provider,provider_customer_id,org_id FROM subscriptions WHERE provider_customer_id<>'' ON CONFLICT DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS billing_accounts;
DROP TABLE IF EXISTS identity_organizations;
DROP TABLE IF EXISTS identity_subjects;
ALTER TABLE subscriptions DROP CONSTRAINT subscriptions_provider_not_empty;
ALTER TABLE subscriptions DROP COLUMN provider;
ALTER TABLE webhook_events DROP CONSTRAINT webhook_events_provider_check;
ALTER TABLE webhook_events ADD CONSTRAINT webhook_events_provider_check CHECK (provider IN ('clerk','polar'));
ALTER TABLE idempotency_keys RENAME COLUMN org_id TO clerk_org_id;
ALTER TABLE notification_preferences RENAME COLUMN user_id TO clerk_user_id;
ALTER TABLE flag_overrides RENAME COLUMN org_id TO clerk_org_id;
ALTER TABLE usage_events RENAME COLUMN org_id TO clerk_org_id;
ALTER TABLE webhook_deliveries RENAME COLUMN org_id TO clerk_org_id;
ALTER TABLE webhook_endpoints RENAME COLUMN org_id TO clerk_org_id;
ALTER TABLE notifications RENAME COLUMN user_id TO clerk_user_id;
ALTER TABLE notifications RENAME COLUMN org_id TO clerk_org_id;
ALTER TABLE schedules RENAME COLUMN org_id TO clerk_org_id;
ALTER TABLE files RENAME COLUMN org_id TO clerk_org_id;
ALTER TABLE api_tokens RENAME COLUMN org_id TO clerk_org_id;
ALTER TABLE projects RENAME COLUMN org_id TO clerk_org_id;
ALTER TABLE audit_log RENAME COLUMN user_id TO clerk_user_id;
ALTER TABLE audit_log RENAME COLUMN org_id TO clerk_org_id;
ALTER TABLE subscriptions RENAME COLUMN provider_customer_id TO polar_customer_id;
ALTER TABLE subscriptions RENAME COLUMN provider_subscription_id TO polar_subscription_id;
ALTER TABLE subscriptions RENAME COLUMN org_id TO clerk_org_id;
ALTER TABLE org_members RENAME COLUMN user_id TO clerk_user_id;
ALTER TABLE org_members RENAME COLUMN org_id TO clerk_org_id;
ALTER TABLE orgs RENAME COLUMN org_id TO clerk_org_id;
ALTER TABLE users RENAME COLUMN user_id TO clerk_user_id;
