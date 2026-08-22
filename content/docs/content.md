---
title: Content
description: A database-backed content registry — blog, changelog, and any type you declare — plus docs embedded in the binary.
section: Features
weight: 13
---

There are two content systems and the split is deliberate. **Editable
content** — blog posts, changelog releases, and any other type you register —
lives in Postgres (`content_entries`), is authored at `/admin/content`, and
changes without a commit or a deploy. **Docs** — these pages — are markdown
files embedded into the binary with `go:embed` (`content/embed.go`) and parsed
once at startup: they must version with the code they describe, and staying in
the tree keeps them greppable by coding agents.

## Collections

| Collection | Source                                  | Edited by                    |
|------------|-----------------------------------------|------------------------------|
| Blog       | `content_entries`, kind `post`           | `/admin/content?kind=post`    |
| Changelog  | `content_entries`, kind `release`        | `/admin/content?kind=release` |
| Docs       | `content/docs/*.md` — `title`, `description`, `section`, `weight`, `draft` | a commit |

- Blog posts sort date-descending on the index and in the feed.
- Docs pages group by `section` and order by `weight` — the sidebar is a
  direct reflection of frontmatter. `draft: true` hides a docs page when
  `APP_ENV=production`; drafts render in development.
- Changelog releases sort date-descending onto one page (see below).
- Database entries have no `draft` frontmatter flag: draft is a status on the
  row, and publishing is a button.

## The content-type registry

Every editable collection is one `content.Type` value in
`internal/content/types.go`. Registering a type is a Go change and nothing
else — no migration, no new table, no new handler, no new template. The
admin list, the editor, validation, revisions, publishing, caching, the public
index and detail routes, and feed and sitemap inclusion are all driven off the
declaration. The recipe is in
[Extending GoGoGadget](/docs/extending).

| Field                    | Meaning                                                                                                                      |
|--------------------------|------------------------------------------------------------------------------------------------------------------------------|
| `Kind`                   | stored in `content_entries.kind`; `[a-z][a-z0-9_]*`                                                                          |
| `LabelKey` / `PluralKey` | i18n keys for the singular and plural label                                                                                   |
| `Path`                   | public base URL (`/blog`). **Empty means no public route at all** — admin CRUD plus programmatic reads                        |
| `Mode`                   | `pages` → an index at `Path` and a detail page at `Path/{slug}`; `single` → every entry on one scrollable page, each anchored |
| `Fields`                 | type-specific inputs, stored flat in the entry's `meta` JSONB                                                                 |
| `Slug`                   | the default slug offered for a new entry (`SlugFromTitle`, `SlugFromDate`)                                                   |
| `Feed`                   | include live entries in `/rss.xml`                                                                                            |
| `Sitemap`                | include live entries in `/sitemap.xml`                                                                                        |

Two types ship: `post` (`/blog`, `pages` mode, feed and sitemap, one `author`
field) and `release` (`/changelog`, `single` mode, sitemap only). `kind`
carries no CHECK constraint in the database on purpose — the registry is the
validator, so a new type never needs DDL. An unregistered kind is an error
rather than a silently empty page, and `content.NewRegistry` refuses a
duplicate kind, a bad identifier, a missing label key, or a `Path` that
collides with a reserved route.

## Publishing, scheduling, expiry

An entry is live when:

```sql
status = 'published'
  AND published_at <= now()
  AND (unpublish_at IS NULL OR unpublish_at > now())
```

That one predicate is the entire publishing model, which is why there is no
background job to configure, monitor, or dead-letter. A **future**
`published_at` *is* the scheduled state; a **past** `unpublish_at` *is* the
expired state. Both transitions happen because `now()` moved, and reach the
site within one cache TTL.

`/admin/content` shows the four states as a **computed** badge — draft,
scheduled, expired, live — never a stored one, so it cannot drift from what
the public site does. An expired entry stays listed and editable for staff
while being absent everywhere else.

## Caching

`content.CMS` maps live rows to view models and caches them per
`(kind, locale)` for **30 seconds**, loaded lazily on the first read of that
pair. Every admin mutation calls `Invalidate()`, so authoring is immediate:
publish, reload, it is there. Only the clock-driven transitions wait out the
TTL. On a query error the previously cached slice is served and the next
request retries, so a database blip degrades to slightly stale content instead
of an error page.

The cached set is capped at **500 live entries per (kind, locale)**. That cap
is also the ceiling for the index, the feed, and the sitemap, because all
three read the same cache. A collection that outgrows it wants pagination,
which is a bigger change than raising a constant.

## Language variants

Every entry carries a `locale`. `''` serves every language; a row with `es`
wins over the `''` row for Spanish readers on the same `(kind, slug)`. The
unique index is `(kind, slug, locale)`, so one slug holds at most one row per
language, and an index lists a slug exactly once whichever language you read
it in.

This is a variant mechanism, not a translation workflow: there is no status
tracking, no side-by-side editing, and no stale-translation warning. A variant
either exists or it does not, and the `''` fallback always does. See
[Internationalization](/docs/i18n).

## The seed corpus

`content/blog/*.md` and `content/changelog/*.md` are still in the repo and
still embedded, but nothing reads them at request time — they are fixtures.
`make seed` imports them into `content_entries`, idempotently by
`(kind, slug)`: an existing row is **never** overwritten, so re-seeding a
database an operator has since edited cannot clobber their work. That is what
makes a fresh clone render a populated site with no manual authoring, and it
keeps the docs link-checker walking the same files.

## Search

The docs sidebar carries a search box (`GET /docs/search?q=`). Scoring lives
in `internal/content/search.go`, over the markdown source (not the rendered
HTML): whitespace-split terms with **AND semantics** — every term must hit
title, description, or body, the same contract as the projects search. Title
hits weigh 50, description 10, body frequency 2 (capped at 10 hits per term
so a keyword-spam page can't dominate); ties keep `weight` order. Each result
carries a cleaned snippet windowed around the longest term. The collection is
28 static embedded pages, so a linear scan per query is exact and fast — no
index to keep in sync; content changes are live on the next boot by
construction.

Database entries are not in that index. Admin search over `content_entries`
is a Postgres full-text query with an ILIKE fallback, the same shape as the
projects search (see [Database](/docs/database)).

## Rendering

Bodies render through goldmark with the GFM extension and **default renderer
options**: raw HTML in markdown stays escaped. Never enable
`html.WithUnsafe` — rendered content is never trusted (see
[Security](/docs/security)). The `.prose` component classes in `input.css`
style the output — no Tailwind typography plugin, so the standalone toolchain
stays JS-free.

Database entries render **at write time**: the editor stores both `body_md`
and `body_html`, so the read path never runs goldmark and what the preview
pane showed is byte-for-byte what publishes. It is the same `content.Render`
on both sides, which is the point.

## Routes and SEO

| Route                       | Content                                                        |
|-----------------------------|----------------------------------------------------------------|
| `/blog`, `/blog/{slug}`     | Index + posts (unknown slug → 404)                             |
| `/changelog`                | Every release on one page, newest first                        |
| `Path`, `Path/{slug}`       | Any registered type, generated from the registry               |
| `/media/{id}/{filename}`    | An uploaded image, served inline                               |
| `/docs`                     | 303 → the lowest-weight page                                   |
| `/docs/{slug}`              | Docs layout: sidebar, prev/next footer, edit link              |
| `/rss.xml`                  | Hand-rolled RSS 2.0 of every `Feed: true` type                 |
| `/sitemap.xml`              | Static routes + every live entry of a `Sitemap: true` type + every docs page |
| `/robots.txt`               | Allow all + sitemap pointer                                    |

Blog and changelog keep bespoke public markup; a type with neither gets
generic index, detail, and single-page templates, and inherits canonical and
`hreflang` tags from the public layout for free. The feed and the sitemap
resolve at the default locale, so each URL appears once and language versions
stay expressed through `hreflang` rather than duplicate entries.

Every page gets Open Graph/Twitter meta from a shared partial (`og:title`,
`og:description`, `og:url`, `og:image` → `/static/og.png`, `twitter:card`).

## Media

Images are uploaded at `/admin/media` and stored **platform-scoped** — not in
the org-scoped `files` table — through the storage seam: DevStore writes under
`tmp/uploads/` locally, R2 in production (see
[File storage](/docs/storage)). The allowlist is PNG, JPEG, GIF, and WebP,
sniffed from the first bytes with `http.DetectContentType` and never taken
from the client's part header. SVG is excluded deliberately: it can carry
script, and it would execute same-origin when served inline.

Media serves at `/media/{id}/{filename}` with an **inline** disposition — the
one place the storage seam renders rather than downloads, which is exactly why
the allowlist is the gate — plus a one-year immutable `Cache-Control` (rows
are immutable and keys are random). The `{filename}` segment is cosmetic. The
editor's copy button hands you `![alt](/media/{id}/{filename})` to paste into
a body.

## Adding a post or a docs page

A post is an authoring task, not a code change: `/admin/content/new?kind=post`
— title, a slug that auto-fills from it, summary, author, markdown body with a
live preview, and optional publish and expiry times. Save as a draft, publish
when ready, and it is on `/blog` on the very next request. Every save
snapshots a revision, so the previous version is one restore away. See
[Admin](/docs/admin).

A docs page is a file:

1. Create `content/docs/my-page.md` with frontmatter, picking the `section`
   and a `weight` that slots into the sidebar order.
2. Restart the server. Docs are embedded and parsed at startup (air
   deliberately excludes `content/`), so a reboot picks it up; the sidebar,
   search, and sitemap update automatically.

Internal `/docs/…` links are enforced by a test
(`TestDocsInternalLinksResolve` in `internal/content`): linking to a slug
that does not exist fails the build.

## Changelog

The changelog is the `release` type: one entry per release, slugged by date
(`SlugFromDate`), rendered together at **`/changelog`**, newest first. That is
`single` mode — every entry on one page — and it is a mode rather than a
special case because a changelog is *read* differently: you scan backwards
until you reach the version you were on, so per-release URLs would be pure
overhead. Each entry still carries a stable anchor, so support can link at the
exact release that fixed someone's problem.

Anchors are `release-2026-08-19`, not `2026-08-19`. A bare date is a legal
HTML id but **not** a legal CSS selector — ids cannot start with a digit, so
`document.querySelector("#2026-08-19")` throws and no stylesheet can target
it. Anchors are therefore `<kind>-<slug>` for every `single` type, and the
prefix removes the trap for free.

The changelog is also the one marketing page with an honest `lastmod` in the
sitemap — the date of its newest live release.
