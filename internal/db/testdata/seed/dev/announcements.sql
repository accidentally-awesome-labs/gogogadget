-- Demo announcement: one active info banner in the app shell. The partial
-- unique index (active) WHERE active guarantees at most one live row, so the
-- conflict target must repeat that predicate to be inferred.
INSERT INTO announcements (kind, message, url, active) VALUES
  ('info', 'Welcome to your new GoGoGadget dev stack', '', TRUE)
ON CONFLICT (active) WHERE active DO NOTHING;
