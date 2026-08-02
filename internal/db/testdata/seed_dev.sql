-- Demo data for local development. Pair with DEV_AUTH_BYPASS=true and log in
-- with cookie __session=e2e:user_demo:org_demo:org:admin
INSERT INTO users (clerk_user_id, email, name, avatar_url) VALUES
  ('user_demo', 'demo@gogogadget.dev', 'Demo User', '')
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
