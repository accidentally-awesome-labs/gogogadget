---
title: SEO
description: Canonicals, hreflang, structured data, sitemap, and feed discovery.
section: Features
weight: 14
---

The marketing surface (`/`, `/pricing`, `/terms`, `/privacy`, `/blog`, `/docs`)
is the only part of the app a crawler ever sees — `/app` and `/admin` sit
behind auth, so they carry no SEO metadata at all.

## Canonicals: the duplicate the locale switcher created

Language is chosen by `?lang=`, then a cookie, then `Accept-Language`. That
means every public page is reachable at three URLs with the same content:

```
/pricing
/pricing?lang=en
/pricing?lang=es
```

Left alone that is textbook duplicate content, and it arrived the day the
switcher shipped. Every public page now emits a **self-referential canonical**
that keeps a supported non-default language and drops everything else:

| Request | Canonical |
|---|---|
| `/pricing` | `/pricing` |
| `/pricing?lang=en` | `/pricing` (the default locale owns the bare path) |
| `/pricing?lang=es` | `/pricing?lang=es` (a translation is its own URL) |
| `/pricing?utm_source=x` | `/pricing` (tracking cannot fork a page) |
| `/pricing?lang=klingon` | `/pricing` (unsupported → the default) |

## hreflang

Each page lists every language version plus `x-default`, and the sets are
**reciprocal** — a one-way hreflang is discarded by search engines, so
`/blog` and `/blog?lang=es` advertise exactly the same three links.

Cookie-negotiated content deliberately gets no URL of its own: a crawler has
no cookie, so it would only ever see the default anyway.

## Structured data

JSON-LD, rendered through `templ.JSONScript` (which marshals and escapes the
value, so a title containing `</script>` cannot break out of the block):

- **`/`** — `Organization` plus a `WebSite` whose `SearchAction` points at
  `/docs/search?q=…`, a search endpoint that actually exists. A test fetches
  it, because advertising a search you do not serve is worse than none.
- **Blog posts** — `BlogPosting` with headline, description, `datePublished`,
  author, and publisher.

This is a **data block, not executable script**: CSP `script-src 'self'` does
not apply to non-JavaScript script types, so the strict policy stays intact.

## Sitemap and feed

`/sitemap.xml` lists the marketing pages, every blog post, and every docs
page. `lastmod` is emitted **only for blog posts** — the one collection with a
real date in its frontmatter. Docs pages ship with the binary and marketing
copy has no timestamp; a fabricated `lastmod` (build time, "today") teaches
crawlers to distrust the field, which is worse than omitting it.

`/rss.xml` has existed since the first release, but nothing linked to it.
Public pages now carry `<link rel="alternate" type="application/rss+xml">`, so
browsers and readers can discover the feed from any page.

`/robots.txt` points at the sitemap.
