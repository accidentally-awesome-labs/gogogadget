-- System schedules: usage metering flushes to Polar every 5 minutes (the
-- jobs worker claims due rows and enqueues their kind); the digest pass runs
-- hourly and decides per user who is actually due (users.digest_frequency).
INSERT INTO schedules (name, kind, payload, org_id, every_seconds, next_run_at, enabled)
SELECT v.name, v.kind, v.payload, v.org_id, v.every_seconds, v.next_run_at, v.enabled
FROM (VALUES
  ('usage-flush', 'usage.flush', '{}'::jsonb, NULL::text, 300, now(), TRUE),
  ('email-digest', 'email.digest', '{}'::jsonb, NULL::text, 3600, now(), TRUE)
) AS v(name, kind, payload, org_id, every_seconds, next_run_at, enabled)
WHERE NOT EXISTS (SELECT 1 FROM schedules s WHERE s.name = v.name);
