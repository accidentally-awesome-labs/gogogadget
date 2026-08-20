-- +goose Up
-- Server-side appearance preferences.
--
-- users.locale has existed since 0002 and is read when rendering the welcome
-- email — but nothing ever wrote it, so it was always '' and that email was
-- always English. This migration adds the missing half (theme) and the app
-- finally persists both.
--
-- locale '' still means "follow the browser" (Accept-Language), which is the
-- right default for a user who never expressed a choice: an explicit 'en'
-- would override a Spanish browser forever.
ALTER TABLE users
  ADD COLUMN theme TEXT NOT NULL DEFAULT 'system'
    CHECK (theme IN ('system', 'light', 'dark'));

-- +goose Down
ALTER TABLE users DROP COLUMN IF EXISTS theme;
