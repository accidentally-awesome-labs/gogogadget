-- Demo data for local development. Pair with DEV_AUTH_BYPASS=true and log in
-- with cookie __session=e2e:user_demo:org_demo:org:admin
-- admin_role is seeded directly: the ADMIN_EMAIL grant only fires on FIRST SIGHT
-- of a missing row (sessionLoad upsert path), so a pre-seeded user_demo would
-- otherwise never reach /admin after make db-reset.
INSERT INTO users (user_id, email, name, avatar_url, admin_role) VALUES
  ('user_demo', 'demo@gogogadget.dev', 'Demo User', '', 'admin')
ON CONFLICT (user_id) DO NOTHING;

INSERT INTO orgs (org_id, name, slug) VALUES
  ('org_demo', 'Demo Org', 'demo-org')
ON CONFLICT (org_id) DO NOTHING;

INSERT INTO org_members (org_id, user_id, role) VALUES
  ('org_demo', 'user_demo', 'org:admin')
ON CONFLICT DO NOTHING;
