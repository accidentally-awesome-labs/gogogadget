-- Demo data for local development. Pair with DEV_AUTH_BYPASS=true and log in
-- with cookie __session=e2e:user_demo:org_demo:org:admin
-- admin_role is seeded directly: the ADMIN_EMAIL grant only fires on FIRST SIGHT
-- of a missing row (sessionLoad upsert path), so a pre-seeded user_demo would
-- otherwise never reach /admin after make db-reset.
INSERT INTO users (clerk_user_id, email, name, avatar_url, admin_role) VALUES
  ('user_demo', 'demo@gogogadget.dev', 'Demo User', '', 'admin')
ON CONFLICT (clerk_user_id) DO NOTHING;

INSERT INTO orgs (clerk_org_id, name, slug) VALUES
  ('org_demo', 'Demo Org', 'demo-org')
ON CONFLICT (clerk_org_id) DO NOTHING;

INSERT INTO org_members (clerk_org_id, clerk_user_id, role) VALUES
  ('org_demo', 'user_demo', 'org:admin')
ON CONFLICT DO NOTHING;

-- `make seed` is re-runnable, so every insert below keys off natural identity.
-- projects, notifications and schedules carry only an identity primary key, so
-- a bare ON CONFLICT DO NOTHING would catch nothing and duplicate the fixture
-- on every run; NOT EXISTS is the guard that actually holds.
INSERT INTO projects (clerk_org_id, name)
SELECT v.clerk_org_id, v.name FROM (VALUES
  ('org_demo', 'Launch checklist'),
  ('org_demo', 'Marketing site refresh'),
  ('org_demo', 'Mobile app spike'),
  ('org_demo', 'Q3 roadmap')
) AS v(clerk_org_id, name)
WHERE NOT EXISTS (
  SELECT 1 FROM projects p WHERE p.clerk_org_id = v.clerk_org_id AND p.name = v.name
);

-- System schedules: usage metering flushes to Polar every 5 minutes (the
-- jobs worker claims due rows and enqueues their kind); the digest pass runs
-- hourly and decides per user who is actually due (users.digest_frequency).
INSERT INTO schedules (name, kind, payload, clerk_org_id, every_seconds, next_run_at, enabled)
SELECT v.name, v.kind, v.payload, v.clerk_org_id, v.every_seconds, v.next_run_at, v.enabled
FROM (VALUES
  ('usage-flush', 'usage.flush', '{}'::jsonb, NULL::text, 300, now(), TRUE),
  ('email-digest', 'email.digest', '{}'::jsonb, NULL::text, 3600, now(), TRUE)
) AS v(name, kind, payload, clerk_org_id, every_seconds, next_run_at, enabled)
WHERE NOT EXISTS (SELECT 1 FROM schedules s WHERE s.name = v.name);

-- Demo notifications: two unread + one read for the demo user.
INSERT INTO notifications (clerk_org_id, clerk_user_id, kind, title, body, url, read_at)
SELECT v.clerk_org_id, v.clerk_user_id, v.kind, v.title, v.body, v.url, v.read_at
FROM (VALUES
  ('org_demo', 'user_demo', 'welcome', 'Welcome to GoGoGadget', 'Create your first project, invite your team, and upgrade when you outgrow the free plan.', '/app', NULL::timestamptz),
  ('org_demo', 'user_demo', 'system', 'Storage is now available', 'Uploads are org-scoped and metered against your plan.', '/app/files', NULL::timestamptz),
  ('org_demo', 'user_demo', 'system', 'Scheduled jobs are live', 'Recurring work runs on the Postgres queue — no daemon needed.', '/docs/background-jobs', now())
) AS v(clerk_org_id, clerk_user_id, kind, title, body, url, read_at)
WHERE NOT EXISTS (
  SELECT 1 FROM notifications n
  WHERE n.clerk_user_id = v.clerk_user_id AND n.kind = v.kind AND n.title = v.title
);

-- Feature flags: webhooks + notifications on for everyone; beta_search is the
-- placeholder builders rename.
INSERT INTO feature_flags (key, description, enabled, rollout) VALUES
  ('webhooks', 'Outbound webhook endpoints + deliveries (Settings → Webhooks)', TRUE, 100),
  ('notifications', 'In-app notifications bell + SSE stream', TRUE, 100),
  ('beta_search', 'Placeholder beta flag — rename for your gated feature', FALSE, 0)
ON CONFLICT (key) DO NOTHING;

-- Demo announcement: one active info banner in the app shell. The partial
-- unique index (active) WHERE active guarantees at most one live row, so the
-- conflict target must repeat that predicate to be inferred.
INSERT INTO announcements (kind, message, url, active) VALUES
  ('info', 'Welcome to your new GoGoGadget dev stack', '', TRUE)
ON CONFLICT (active) WHERE active DO NOTHING;
