-- E2E fixtures: fixed timestamps (2026-01-15) throughout for visual
-- determinism. Loaded by playwright globalSetup via `go run ./cmd/seed -reset`.
INSERT INTO users (user_id, email, name, avatar_url, admin_role, disabled_at, created_at, updated_at) VALUES
  ('user_free',     'free@gogogadget.dev',     'Free User',     '', '',      NULL,                        '2026-01-15T00:00:00Z', '2026-01-15T00:00:00Z'),
  ('user_pro',      'pro@gogogadget.dev',      'Pro User',      '', '',      NULL,                        '2026-01-15T00:00:00Z', '2026-01-15T00:00:00Z'),
  ('user_admin',    'admin@gogogadget.dev',    'Site Admin',    '', 'admin', NULL,                        '2026-01-15T00:00:00Z', '2026-01-15T00:00:00Z'),
  ('user_disabled', 'disabled@gogogadget.dev', 'Disabled User', '', '',      '2026-01-15T00:00:00Z',      '2026-01-15T00:00:00Z', '2026-01-15T00:00:00Z'),
  ('user_noorg',    'noorg@gogogadget.dev',    'No Org',        '', '',      NULL,                        '2026-01-15T00:00:00Z', '2026-01-15T00:00:00Z'),
  ('user_noactive', 'noactive@gogogadget.dev', 'No Active Org', '', '',      NULL,                        '2026-01-15T00:00:00Z', '2026-01-15T00:00:00Z'),
  ('user_toggle',   'toggle@gogogadget.dev',   'Toggle Target', '', '',      NULL,                        '2026-01-15T00:00:00Z', '2026-01-15T00:00:00Z'),
  ('user_deleteme', 'deleteme@example.com',    'Delete Me',     '', '',      NULL,                        '2026-01-15T00:00:00Z', '2026-01-15T00:00:00Z'),
  ('user_support',  'support@gogogadget.dev',  'Support Staff', '', 'support', NULL,                      '2026-01-15T00:00:00Z', '2026-01-15T00:00:00Z')
ON CONFLICT (user_id) DO NOTHING;

INSERT INTO orgs (org_id, name, slug, image_url, created_at, updated_at) VALUES
  ('org_free', 'Free Org', 'free-org', '', '2026-01-15T00:00:00Z', '2026-01-15T00:00:00Z'),
  ('org_pro',  'Pro Org',  'pro-org',  '', '2026-01-15T00:00:00Z', '2026-01-15T00:00:00Z'),
  ('org_deleteme', 'Delete Me Org', 'delete-me-org', '', '2026-01-15T00:00:00Z', '2026-01-15T00:00:00Z')
ON CONFLICT (org_id) DO NOTHING;

INSERT INTO org_members (org_id, user_id, role, created_at) VALUES
  ('org_free', 'user_free',     'org:member', '2026-01-15T00:00:00Z'),
  ('org_free', 'user_admin',    'org:admin',  '2026-01-15T00:00:00Z'),
  ('org_free', 'user_disabled', 'org:member', '2026-01-15T00:00:00Z'),
  ('org_free', 'user_noactive', 'org:member', '2026-01-15T00:00:00Z'),
  ('org_free', 'user_toggle',   'org:member', '2026-01-15T00:00:00Z'),
  ('org_pro',  'user_pro',      'org:admin',  '2026-01-15T00:00:00Z'),
  ('org_deleteme', 'user_deleteme', 'org:admin', '2026-01-15T00:00:00Z')
ON CONFLICT DO NOTHING;
