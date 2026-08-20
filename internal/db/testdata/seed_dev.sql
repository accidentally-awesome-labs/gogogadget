-- Demo data for local development. Pair with DEV_AUTH_BYPASS=true and log in
-- with cookie __session=e2e:user_demo:org_demo:org:admin
-- is_admin is seeded directly: the ADMIN_EMAIL grant only fires on FIRST SIGHT
-- of a missing row (sessionLoad upsert path), so a pre-seeded user_demo would
-- otherwise never reach /admin after make db-reset.
INSERT INTO users (clerk_user_id, email, name, avatar_url, is_admin) VALUES
  ('user_demo', 'demo@gogogadget.dev', 'Demo User', '', TRUE)
ON CONFLICT (clerk_user_id) DO NOTHING;

INSERT INTO orgs (clerk_org_id, name, slug) VALUES
  ('org_demo', 'Demo Org', 'demo-org')
ON CONFLICT (clerk_org_id) DO NOTHING;

INSERT INTO org_members (clerk_org_id, clerk_user_id, role) VALUES
  ('org_demo', 'user_demo', 'org:admin')
ON CONFLICT DO NOTHING;

INSERT INTO projects (clerk_org_id, name) VALUES
  ('org_demo', 'Launch checklist'),
  ('org_demo', 'Marketing site refresh'),
  ('org_demo', 'Mobile app spike'),
  ('org_demo', 'Q3 roadmap')
ON CONFLICT DO NOTHING;

-- System schedules: usage metering flushes to Polar every 5 minutes (the
-- jobs worker claims due rows and enqueues their kind); the digest pass runs
-- hourly and decides per user who is actually due (users.digest_frequency).
INSERT INTO schedules (name, kind, payload, clerk_org_id, every_seconds, next_run_at, enabled) VALUES
  ('usage-flush', 'usage.flush', '{}', NULL, 300, now(), TRUE),
  ('email-digest', 'email.digest', '{}', NULL, 3600, now(), TRUE)
ON CONFLICT DO NOTHING;

-- Demo notifications: two unread + one read for the demo user.
INSERT INTO notifications (clerk_org_id, clerk_user_id, kind, title, body, url, read_at) VALUES
  ('org_demo', 'user_demo', 'welcome', 'Welcome to GoGoGadget', 'Create your first project, invite your team, and upgrade when you outgrow the free plan.', '/app', NULL),
  ('org_demo', 'user_demo', 'system', 'Storage is now available', 'Uploads are org-scoped and metered against your plan.', '/app/files', NULL),
  ('org_demo', 'user_demo', 'system', 'Scheduled jobs are live', 'Recurring work runs on the Postgres queue — no daemon needed.', '/docs/background-jobs', now())
ON CONFLICT DO NOTHING;

-- Feature flags: webhooks + notifications on for everyone; beta_search is the
-- placeholder builders rename.
INSERT INTO feature_flags (key, description, enabled, rollout) VALUES
  ('webhooks', 'Outbound webhook endpoints + deliveries (Settings → Webhooks)', TRUE, 100),
  ('notifications', 'In-app notifications bell + SSE stream', TRUE, 100),
  ('beta_search', 'Placeholder beta flag — rename for your gated feature', FALSE, 0)
ON CONFLICT (key) DO NOTHING;

-- Demo announcement: one active info banner in the app shell (the partial
-- unique index guarantees at most one active row).
INSERT INTO announcements (kind, message, url, active) VALUES
  ('info', 'Welcome to your new GoGoGadget dev stack', '', TRUE);
