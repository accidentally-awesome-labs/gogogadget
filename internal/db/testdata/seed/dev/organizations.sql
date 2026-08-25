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
