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
