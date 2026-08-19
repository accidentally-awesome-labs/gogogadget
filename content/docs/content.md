---
title: Content
description: Markdown blog and docs collections, rendered in-app and embedded in the binary.
section: Features
weight: 13
---

Blog posts and these docs are markdown files in the repo, embedded into the
binary with `go:embed` (`content/embed.go`) and parsed once at startup by
`internal/content`. Docs ship IN the app: they version with the code and are
greppable by coding agents.

## Collections

| Collection | Path                 | Frontmatter                                          |
|------------|----------------------|------------------------------------------------------|
| Blog       | `content/blog/*.md`  | `title`, `description`, `date`, `author`, `draft`    |
| Docs       | `content/docs/*.md`  | `title`, `description`, `section`, `weight`, `draft` |

- Blog posts sort date-descending on the index and in the feed.
- Docs pages group by `section` and order by `weight` — the sidebar is a
  direct reflection of frontmatter.
- `draft: true` hides the page when `APP_ENV=production`; drafts render in
  development.

## Search

The docs sidebar carries a search box (`GET /docs/search?q=`). Scoring lives
in `internal/content/search.go`, over the markdown source (not the rendered
HTML): whitespace-split terms with **AND semantics** — every term must hit
title, description, or body, the same contract as the projects search. Title
hits weigh 50, description 10, body frequency 2 (capped at 10 hits per term
so a keyword-spam page can't dominate); ties keep `weight` order. Each result
carries a cleaned snippet windowed around the longest term. The collection is
27 static embedded pages, so a linear scan per query is exact and fast — no
index to keep in sync; content changes are live on the next boot by
construction.

## Rendering

Bodies render through goldmark with the GFM extension and **default renderer
options**: raw HTML in markdown stays escaped. Never enable
`html.WithUnsafe` — rendered content is never trusted (see
[Security](/docs/security)). The `.prose` component classes in `input.css`
style the output — no Tailwind typography plugin, so the standalone toolchain
stays JS-free.

## Routes and SEO

| Route                 | Content                                              |
|-----------------------|------------------------------------------------------|
| `/blog`, `/blog/{slug}` | Index + posts (unknown slug → 404)                 |
| `/docs`               | 303 → the lowest-weight page                         |
| `/docs/{slug}`        | Docs layout: sidebar, prev/next footer, edit link    |
| `/rss.xml`            | Hand-rolled RSS 2.0 of the blog                      |
| `/sitemap.xml`        | Static routes + every post + every docs page         |
| `/robots.txt`         | Allow all + sitemap pointer                          |

Every page gets Open Graph/Twitter meta from a shared partial (`og:title`,
`og:description`, `og:url`, `og:image` → `/static/og.png`, `twitter:card`).

## Adding a post or docs page

1. Create the file with frontmatter — `content/blog/my-post.md` or
   `content/docs/my-page.md`. For docs, pick the `section` and a `weight`
   that slots into the sidebar order.
2. Restart the server. Content is embedded and parsed at startup (air
   deliberately excludes `content/`), so a reboot picks it up; the index,
   sidebar, feed, and sitemap update automatically.

Internal `/docs/…` links are enforced by a test
(`TestDocsInternalLinksResolve` in `internal/content`): linking to a slug
that does not exist fails the build.
