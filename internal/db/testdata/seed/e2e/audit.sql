-- A page+ of audit rows for org_pro (relative times render off TEST_NOW).
INSERT INTO audit_log (org_id, user_id, action, metadata, created_at)
SELECT 'org_pro', 'user_pro', 'project.created',
       jsonb_build_object('name', 'Project ' || g),
       '2026-01-15T00:00:00Z'::timestamptz + (g || ' minutes')::interval
FROM generate_series(1, 25) AS g;
