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
