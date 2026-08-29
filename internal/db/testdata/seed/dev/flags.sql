-- Feature flags: webhooks + notifications on for everyone; beta_search is the
-- placeholder builders rename.
INSERT INTO feature_flags (key, description, enabled, rollout) VALUES
  ('webhooks', 'Outbound webhook endpoints + deliveries (Settings → Webhooks)', TRUE, 100),
  ('notifications', 'In-app notifications bell + SSE stream', TRUE, 100),
  ('beta_search', 'Placeholder beta flag — rename for your gated feature', FALSE, 0)
ON CONFLICT (key) DO NOTHING;
