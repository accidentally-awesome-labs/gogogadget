-- content_entries (every registered content type; live = published AND published_at <= now() AND not expired)

-- name: ListLiveEntries :many
-- One row per slug: the requested locale wins, the '' (all-languages) row is
-- the fallback. DISTINCT ON forces ORDER BY slug first; callers re-sort by date.
SELECT DISTINCT ON (slug) * FROM content_entries
WHERE kind = sqlc.arg(kind)
  AND status = 'published'
  AND published_at <= now()
  AND (unpublish_at IS NULL OR unpublish_at > now())
  AND locale IN (sqlc.arg(locale), '')
ORDER BY slug, (locale = sqlc.arg(locale)) DESC
LIMIT sqlc.arg(lim);

-- name: GetLiveEntry :one
SELECT * FROM content_entries
WHERE kind = sqlc.arg(kind)
  AND slug = sqlc.arg(slug)
  AND status = 'published'
  AND published_at <= now()
  AND (unpublish_at IS NULL OR unpublish_at > now())
  AND locale IN (sqlc.arg(locale), '')
ORDER BY (locale = sqlc.arg(locale)) DESC
LIMIT 1;

-- name: LatestLiveEntry :one
SELECT * FROM content_entries
WHERE kind = sqlc.arg(kind)
  AND status = 'published'
  AND published_at <= now()
  AND (unpublish_at IS NULL OR unpublish_at > now())
  AND locale IN (sqlc.arg(locale), '')
ORDER BY published_at DESC
LIMIT 1;

-- The importer's existence check: any status, exact locale.
-- name: GetEntryByKindSlugLocale :one
SELECT * FROM content_entries WHERE kind = $1 AND slug = $2 AND locale = $3;

-- name: ListEntriesAdmin :many
-- FTS (websearch syntax) with an ILIKE fallback so partial tokens still match.
-- Every locale variant is its own row; the admin table shows a locale column.
SELECT * FROM content_entries
WHERE (sqlc.arg(kind)::text = '' OR kind = sqlc.arg(kind))
  AND (sqlc.arg(filter)::text = '' OR search_tsv @@ websearch_to_tsquery('simple', sqlc.arg(filter)) OR title ILIKE '%' || sqlc.arg(filter) || '%')
ORDER BY COALESCE(published_at, created_at) DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: CountEntriesAdmin :one
SELECT count(*) FROM content_entries
WHERE (sqlc.arg(kind)::text = '' OR kind = sqlc.arg(kind))
  AND (sqlc.arg(filter)::text = '' OR search_tsv @@ websearch_to_tsquery('simple', sqlc.arg(filter)) OR title ILIKE '%' || sqlc.arg(filter) || '%');

-- name: GetEntry :one
SELECT * FROM content_entries WHERE id = $1;

-- name: CreateEntry :one
INSERT INTO content_entries (kind, slug, locale, title, summary, body_md, body_html, meta, status, published_at, unpublish_at, created_by)
VALUES (
  sqlc.arg(kind), sqlc.arg(slug), sqlc.arg(locale), sqlc.arg(title), sqlc.arg(summary),
  sqlc.arg(body_md), sqlc.arg(body_html), sqlc.arg(meta), sqlc.arg(status),
  sqlc.arg(published_at), sqlc.arg(unpublish_at), sqlc.arg(created_by)
)
RETURNING *;

-- name: UpdateEntry :one
UPDATE content_entries SET
  title = sqlc.arg(title),
  slug = sqlc.arg(slug),
  locale = sqlc.arg(locale),
  summary = sqlc.arg(summary),
  body_md = sqlc.arg(body_md),
  body_html = sqlc.arg(body_html),
  meta = sqlc.arg(meta),
  published_at = sqlc.arg(published_at),
  unpublish_at = sqlc.arg(unpublish_at),
  updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: SetEntryStatus :one
UPDATE content_entries SET status = sqlc.arg(status), published_at = sqlc.arg(published_at), updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteEntry :exec
DELETE FROM content_entries WHERE id = $1;
