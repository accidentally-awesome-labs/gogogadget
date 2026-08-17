-- E2E fixtures: fixed timestamps (2026-01-15) throughout for visual
-- determinism. Loaded by playwright globalSetup via `go run ./cmd/seed -reset`.

INSERT INTO users (clerk_user_id, email, name, avatar_url, is_admin, disabled_at, created_at, updated_at) VALUES
  ('user_free',     'free@gogogadget.dev',     'Free User',     '', FALSE, NULL,                        '2026-01-15T00:00:00Z', '2026-01-15T00:00:00Z'),
  ('user_pro',      'pro@gogogadget.dev',      'Pro User',      '', FALSE, NULL,                        '2026-01-15T00:00:00Z', '2026-01-15T00:00:00Z'),
  ('user_admin',    'admin@gogogadget.dev',    'Site Admin',    '', TRUE,  NULL,                        '2026-01-15T00:00:00Z', '2026-01-15T00:00:00Z'),
  ('user_disabled', 'disabled@gogogadget.dev', 'Disabled User', '', FALSE, '2026-01-15T00:00:00Z',      '2026-01-15T00:00:00Z', '2026-01-15T00:00:00Z'),
  ('user_noorg',    'noorg@gogogadget.dev',    'No Org',        '', FALSE, NULL,                        '2026-01-15T00:00:00Z', '2026-01-15T00:00:00Z'),
  ('user_noactive', 'noactive@gogogadget.dev', 'No Active Org', '', FALSE, NULL,                        '2026-01-15T00:00:00Z', '2026-01-15T00:00:00Z'),
  ('user_toggle',   'toggle@gogogadget.dev',   'Toggle Target', '', FALSE, NULL,                        '2026-01-15T00:00:00Z', '2026-01-15T00:00:00Z')
ON CONFLICT (clerk_user_id) DO NOTHING;

INSERT INTO orgs (clerk_org_id, name, slug, image_url, created_at, updated_at) VALUES
  ('org_free', 'Free Org', 'free-org', '', '2026-01-15T00:00:00Z', '2026-01-15T00:00:00Z'),
  ('org_pro',  'Pro Org',  'pro-org',  '', '2026-01-15T00:00:00Z', '2026-01-15T00:00:00Z')
ON CONFLICT (clerk_org_id) DO NOTHING;

INSERT INTO org_members (clerk_org_id, clerk_user_id, role, created_at) VALUES
  ('org_free', 'user_free',     'org:member', '2026-01-15T00:00:00Z'),
  ('org_free', 'user_admin',    'org:admin',  '2026-01-15T00:00:00Z'),
  ('org_free', 'user_disabled', 'org:member', '2026-01-15T00:00:00Z'),
  ('org_free', 'user_noactive', 'org:member', '2026-01-15T00:00:00Z'),
  ('org_free', 'user_toggle',   'org:member', '2026-01-15T00:00:00Z'),
  ('org_pro',  'user_pro',      'org:admin',  '2026-01-15T00:00:00Z')
ON CONFLICT DO NOTHING;

-- Pro org: active subscription ending in 30 days (relative to seed time).
INSERT INTO subscriptions (clerk_org_id, polar_subscription_id, polar_customer_id, product_key, status, current_period_end)
VALUES ('org_pro', 'sub_e2e_pro', 'cust_e2e_pro', 'pro', 'active', now() + interval '30 days')
ON CONFLICT (clerk_org_id) DO NOTHING;

INSERT INTO projects (clerk_org_id, name, created_at, updated_at) VALUES
  ('org_pro', 'Alpha',   '2026-01-15T00:00:00Z', '2026-01-15T00:00:00Z'),
  ('org_pro', 'Bravo',   '2026-01-15T01:00:00Z', '2026-01-15T01:00:00Z'),
  ('org_pro', 'Charlie', '2026-01-15T02:00:00Z', '2026-01-15T02:00:00Z'),
  ('org_pro', 'Delta',   '2026-01-15T03:00:00Z', '2026-01-15T03:00:00Z'),
  ('org_pro', 'Echo',    '2026-01-15T04:00:00Z', '2026-01-15T04:00:00Z'),
  ('org_free', 'One',    '2026-01-15T00:00:00Z', '2026-01-15T00:00:00Z'),
  ('org_free', 'Two',    '2026-01-15T01:00:00Z', '2026-01-15T01:00:00Z'),
  ('org_free', 'Three',  '2026-01-15T02:00:00Z', '2026-01-15T02:00:00Z')
ON CONFLICT DO NOTHING;

-- A page+ of audit rows for org_pro (relative times render off TEST_NOW).
INSERT INTO audit_log (clerk_org_id, clerk_user_id, action, metadata, created_at)
SELECT 'org_pro', 'user_pro', 'project.created',
       jsonb_build_object('name', 'Project ' || g),
       '2026-01-15T00:00:00Z'::timestamptz + (g || ' minutes')::interval
FROM generate_series(1, 25) AS g;

-- Feature flags (gated features must be ON in e2e).
INSERT INTO feature_flags (key, description, enabled, rollout) VALUES
  ('webhooks', 'Outbound webhook endpoints + deliveries', TRUE, 100),
  ('notifications', 'In-app notifications', TRUE, 100)
ON CONFLICT (key) DO NOTHING;

-- Notifications for the pro user (badge visible in e2e).
INSERT INTO notifications (clerk_org_id, clerk_user_id, kind, title, body, url, created_at) VALUES
  ('org_pro', 'user_pro', 'welcome', 'Welcome to GoGoGadget', 'Create your first project.', '/app', '2026-01-15T00:00:00Z'),
  ('org_pro', 'user_pro', 'system', 'Storage is now available', 'Uploads are org-scoped.', '/app/files', '2026-01-15T00:05:00Z')
ON CONFLICT DO NOTHING;
