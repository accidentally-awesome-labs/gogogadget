---
title: Troubleshooting
description: The failures you will actually meet — CSRF 403s, webhook 400s, 503s, and stale baselines.
section: Guides
weight: 20
---

Every failure here has a mechanical cause. Check the exact symptom, not the
vibes.

## 403 on every form POST (CSRF)

- **Stale token.** The CSRF token rides in `body hx-headers:inherited` and the
  cookie rotates. Re-render the page (full reload) and submit again. If you
  cached or hard-coded a token anywhere: don't.
- **You dropped the `:inherited` suffix.** htmx 4 inheritance is explicit: a
  plain `hx-headers` on `<body>` applies to `<body>` only, so every nested form
  posts with no token and 403s. Symptom: the request shows no `X-CSRF-Token`
  header at all. See [Security](/docs/security).
- **Cross-origin request without an `Origin` header.** nosurf v1.2 enforces
  same-origin (CVE-2025-46721). Hand-rolled `curl`/script POSTs that omit
  `Origin` fail — send `Origin: http://localhost:8080`, or use the
  cookieless `/api/*` surface with a Bearer token instead.
- **You're testing an exempt path with a cookie flow.** `/webhooks/*`,
  `/api/*`, `/ingest/*`, `/static/*`, `/healthz`, `/readyz` skip CSRF by
  design; everything else doesn't.
- **Safari in dev.** If you forced production cookie settings on http
  (`__Host-csrf` or `Secure`), Safari silently drops the cookie and every
  POST 403s. Development uses the plain `csrf_token` name on purpose — don't
  override `csrfCookieName`.
- Outside production the 403 page shows the exact `nosurf.Reason`. Read it
  first.

## Webhook 400s

- **Wrong library for the header family.** Clerk delivers via Svix
  (`svix-id`, `svix-timestamp`, `svix-signature`) and verifies with the svix
  library; Polar delivers `webhook-id`, `webhook-timestamp`,
  `webhook-signature` and verifies with standard-webhooks. Point either
  library at the other's traffic and you reject 100% of real deliveries.
- **Wrong secret.** `CLERK_WEBHOOK_SECRET` for `/webhooks/clerk`,
  `POLAR_WEBHOOK_SECRET` for `/webhooks/polar` — they are not interchangeable.
- **Missing message id.** `svix-id` / `webhook-id` is required for
  idempotency; its absence is a 400, not a retry.
- **Re-serialized body.** Verification runs over the **raw** body. Parsing
  and re-encoding before verifying changes the bytes and breaks the
  signature.
- A replayed delivery is **not** an error: duplicates return 200 with no
  side effects (the `webhook_events` row already exists). Don't "fix" that.

## 503 "not configured"

| Symptom | Missing env | Docs |
|---|---|---|
| `/app` routes 503 | `CLERK_SECRET_KEY` (auth unconfigured) | [Authentication](/docs/authentication) |
| Clerk webhook 503 | `CLERK_WEBHOOK_SECRET` | [Organizations](/docs/organizations) |
| Checkout/portal 503 fragment | `POLAR_ACCESS_TOKEN` | [Billing](/docs/billing) |
| Polar webhook 500 at init | `POLAR_WEBHOOK_SECRET` | [Billing](/docs/billing) |

Each 503 fragment links its docs page from the UI. Email, PostHog, and Sentry
never 503 — they degrade to the DevSender (`tmp/emails/*.html`) or a no-op.
In production the four `CLERK_*` keys and `DATABASE_URL` are boot-required,
so you can't reach this state there.

## sqlc / templ compile errors after editing queries or templates

You edited a generated file, or edited the source without regenerating.
Sources are `internal/db/queries/*.sql`, `internal/web/templates/*.templ`,
and `input.css`; outputs are `internal/db/sqlc/`, `*_templ.go`, and
`static/app.css`. **Never edit outputs** — run `make generate`. CI enforces
this with `git diff --exit-code`: if CI is red right after `make generate`,
someone hand-edited generated code.

## Safari drops cookies in local dev

Two separate Safari behaviors: `Secure` cookies are ignored over http, and
`__Host-`-prefixed cookies without `Secure` are rejected outright. This is
why development uses the non-Secure `csrf_token` cookie. The fix is never to
weaken production — it's to not run `APP_ENV=production` on plain-http
localhost.

## Visual test diffs on macOS

Baselines are font-rendering-sensitive and pinned to the **Playwright Linux
container**; a Mac will always diff slightly. Never regenerate baselines
locally — `make visual-update` runs the suite dockerized with
`--update-snapshots`, and CI runs the same pinned image. Rendered dates come
from the frozen `TEST_NOW` clock, so if a diff shows a date, your run is
missing `APP_ENV=test`/`TEST_NOW`. See [Testing](/docs/testing).

## Session dies ~60 seconds after login

`__session` is a ~60-second JWT; the vendored clerk-js refreshes it in the
background. If auth expires a minute after login, clerk-js isn't running:

1. **Browser console shows a CSP-blocked source** — fix
   `CLERK_FRONTEND_API_URL` (it feeds `connect-src`); add only the exact
   reported origin if it's legitimate. See [Security](/docs/security).
2. **No publishable-key meta tag in the page** — `CLERK_PUBLISHABLE_KEY` is
   empty, so layouts skip clerk-js entirely.
3. **Missing vendored file** — re-run `scripts/vendor-frontend.sh` (or
   `make setup`) to restore `static/vendor/clerk.browser.js`.

In e2e/dev bypass mode this is expected — `FakeVerifier` tokens don't
expire, and clerk-js is intentionally absent.

## Layout chrome from the previous page is still on screen

A navigation swaps `#content` and nothing else, so **every page reachable by a
boosted link must render identical chrome around it**. When it doesn't, whatever
differs is left behind (a docs table of contents stranded on `/pricing`) or
never arrives (a missing footer).

Fix the layout, not the link: move the differing markup **inside** `#content`.
That is why `PublicLayout` and `DocsLayout` share one `publicShell` and the docs
sidebar lives in `docsBody`. `TestPublicAndDocsLayoutsShareChrome` diffs
everything outside `#content` between the two layouts, so this fails the build
rather than reaching a browser.

## A dropdown or theme toggle stops working after navigating

Something swapped the shell. clerk-js renders its menus as portals appended
directly to `<body>` — siblings of its mount roots, so no per-element attribute
can protect them — and Alpine bindings in the shell break the same way. Any swap
or morph of `<body>` kills both.

Check that the nav link targets `#content` (`hx-target`/`hx-select`) and that no
response widened it via `HX-Retarget`. `hx-preserve` is not the fix: it stashes
the element and restores it, which is what detaches the listeners in the first
place. See [Frontend](/docs/frontend).

## A navigation lands mid-page instead of at the top

`hx-swap` is missing `show:top`. htmx defaults only boosted **forms** to
scrolling, never links, so a boosted link keeps the previous scroll offset.
`templates.NavSwap` carries it for every nav link. (`scroll:window:top` reads
like the same thing but is a no-op in 4.0.0-beta6.)

## Clicking an in-page anchor flashes the wrong section

The link got boosted. htmx fetches the page, repaints `#content` at the top of
the document, and only then scrolls to the fragment. Links whose `href` contains
a `#` must stay unboosted (`isAnchorLink` in `nav.templ`) — the browser then
scrolls natively with no request at all.

## 429s during load tests

Working as intended: **100 req/min with burst 200 per IP**, 429 responses
carry `Retry-After: 1`. `/static/*`, `/healthz`, and `/ingest/*` are exempt.
From a single load-test host you will trip it — spread client IPs, point the
generator at exempt paths, or take the 429s as proof the limiter works. And
remember it's per-process: two machines double the effective limit until you
swap in a shared store (see [Deployment](/docs/deployment)).
