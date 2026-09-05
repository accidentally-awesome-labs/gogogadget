-- Two content entries that pin the badge states the imported corpus cannot.
--
-- /admin/content computes four states — draft, scheduled, expired, live — from
-- the database clock (ListEntriesAdmin's lifecycle column). The imported blog
-- and changelog corpus is all dated in the past, so it only ever exercises
-- "live"; before the badge moved off the frozen render clock the visual
-- baseline showed ten of eleven rows as "Scheduled", which was the frozen
-- clock lying about content the public site was already serving. These two rows
-- cover the other two states on purpose instead of by accident. "Draft" is
-- covered at the e2e layer, where admin-content.spec.ts asserts it on a row it
-- creates and then publishes.
--
-- The dates are chosen to be DECAY-PROOF, not merely distant. A "far future"
-- date that arrives breaks the baseline for no code reason — the same defect on
-- a longer fuse. Wall clocks only move forward, so a date at the far end of the
-- representable range can never be reached and a date near the epoch can never
-- stop being past. 9999-12-31 stays scheduled and 1970-01-01/02 stays expired
-- for as long as this repository exists, under any clock, in any timezone.
INSERT INTO content_entries
  (kind, slug, locale, title, summary, body_md, body_html, meta, status, published_at, unpublish_at, created_by)
VALUES
  ('post', 'seed-scheduled-post', '', 'Scheduled post fixture',
   'Publishes at the end of the representable calendar, so it is scheduled under any clock.',
   'Scheduled fixture body.', '<p>Scheduled fixture body.</p>', '{}'::jsonb,
   'published', '9999-12-31 00:00:00+00', NULL, 'user_admin'),
  ('post', 'seed-expired-post', '', 'Expired post fixture',
   'Published and retired near the epoch, so it is expired under any clock.',
   'Expired fixture body.', '<p>Expired fixture body.</p>', '{}'::jsonb,
   'published', '1970-01-01 00:00:00+00', '1970-01-02 00:00:00+00', 'user_admin')
ON CONFLICT (kind, slug, locale) DO NOTHING;
