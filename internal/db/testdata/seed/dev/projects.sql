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
