-- Notifications for the pro user (badge visible in e2e).
INSERT INTO notifications (clerk_org_id, clerk_user_id, kind, title, body, url, created_at) VALUES
  ('org_pro', 'user_pro', 'welcome', 'Welcome to GoGoGadget', 'Create your first project.', '/app', '2026-01-15T00:00:00Z'),
  ('org_pro', 'user_pro', 'system', 'Storage is now available', 'Uploads are org-scoped.', '/app/files', '2026-01-15T00:05:00Z')
ON CONFLICT DO NOTHING;

-- No announcements are seeded here on purpose: an active banner would shift
-- every app-page visual baseline.
