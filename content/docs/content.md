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
