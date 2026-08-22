-- +goose Up
-- Editable content for every registered content type (internal/content/types.go).
-- kind is intentionally unconstrained here: registering a new type is a Go
-- change, never a migration. The registry is the validator.
CREATE TABLE content_entries (
  id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  kind         TEXT NOT NULL,
  slug         TEXT NOT NULL,
  -- '' means "serves every language"; 'es' is a Spanish variant that wins over
  -- the '' row for the same (kind, slug) when the request resolves to Spanish.
  locale       TEXT NOT NULL DEFAULT '',
  title        TEXT NOT NULL,
  summary      TEXT NOT NULL DEFAULT '',
  body_md      TEXT NOT NULL,
  body_html    TEXT NOT NULL,
  -- Type-declared extra fields (Type.Fields), flat string values. Not indexed:
  -- a type that needs to query by a field owns a real column or table.
  meta         JSONB NOT NULL DEFAULT '{}',
  status       TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published')),
  -- Visibility is status='published' AND published_at <= now() AND
  -- (unpublish_at IS NULL OR unpublish_at > now()). A future published_at IS the
  -- scheduled state and a past unpublish_at IS the expired state, so neither
  -- publishing nor retiring needs a background job.
  published_at TIMESTAMPTZ,
  unpublish_at TIMESTAMPTZ,
  created_by   TEXT NOT NULL DEFAULT '',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  search_tsv   tsvector GENERATED ALWAYS AS (to_tsvector('simple', title || ' ' || summary || ' ' || body_md)) STORED,
  CONSTRAINT content_entries_published_needs_date CHECK (status <> 'published' OR published_at IS NOT NULL),
  CONSTRAINT content_entries_expiry_after_publish CHECK (unpublish_at IS NULL OR published_at IS NULL OR unpublish_at > published_at)
);
CREATE UNIQUE INDEX content_entries_kind_slug_locale ON content_entries (kind, slug, locale);
CREATE INDEX content_entries_live_idx ON content_entries (kind, published_at DESC) WHERE status = 'published';
CREATE INDEX content_entries_search_idx ON content_entries USING GIN (search_tsv);

-- Every save snapshots the saved state, so the list is a full history and
-- restoring an older row is itself a save.
CREATE TABLE content_revisions (
  id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  entry_id   BIGINT NOT NULL REFERENCES content_entries(id) ON DELETE CASCADE,
  title      TEXT NOT NULL,
  summary    TEXT NOT NULL DEFAULT '',
  body_md    TEXT NOT NULL,
  meta       JSONB NOT NULL DEFAULT '{}',
  editor_id  TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX content_revisions_entry_idx ON content_revisions (entry_id, created_at DESC);

-- Platform-scoped, deliberately NOT the org-scoped files table: files.clerk_org_id
-- is NOT NULL with an FK to orgs, and every query in queries/files.sql filters on it.
CREATE TABLE content_media (
  id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  filename     TEXT NOT NULL,
  content_type TEXT NOT NULL,
  size_bytes   BIGINT NOT NULL,
  storage_key  TEXT NOT NULL UNIQUE,
  alt          TEXT NOT NULL DEFAULT '',
  uploaded_by  TEXT NOT NULL DEFAULT '',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX content_media_created_idx ON content_media (created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS content_media;
DROP TABLE IF EXISTS content_revisions;
DROP TABLE IF EXISTS content_entries;
