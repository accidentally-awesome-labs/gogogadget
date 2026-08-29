-- Feature flags (gated features must be ON in e2e).
INSERT INTO feature_flags (key, description, enabled, rollout) VALUES
  ('webhooks', 'Outbound webhook endpoints + deliveries', TRUE, 100),
  ('notifications', 'In-app notifications', TRUE, 100)
ON CONFLICT (key) DO NOTHING;
