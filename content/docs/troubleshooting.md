---
title: Troubleshooting
description: The failures you will actually meet — CSRF 403s, webhook 400s, 503s, and stale baselines.
section: Guides
weight: 28
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
Outputs are `internal/db/sqlc/`, every `*_templ.go`, every
`*_registry_gen.*`, `static/app.css`, `static/ui-components.js`,
`static/ui-engines.js`, `.env.example`,
`content/docs/configuration-reference.md`,
`content/docs/module-reference.md`, `content/docs/component-reference.md`,
`e2e/generated/personas.ts`,
`e2e/generated/surfaces.ts`, `internal/web/templates/scenarios_gen.go` and
`internal/web/templates/ui/reference_gen.go`. **Never edit outputs** — run
`make generate`. CI enforces it with `git diff --exit-code`: if CI is red
right after `make generate`, someone hand-edited generated code.

## My edit to a route, an env key or a catalog string vanished

You edited a `*_registry_gen.*` file. Those are rendered by `ggg sync` from
the module manifests, so the next `make generate` overwrites them without a
word. Change the declaration instead:

| You wanted to change | Edit this |
|---|---|
| a route, its scope or its CSRF/rate policy | the owning manifest's `runtime.routes` |
| a sidebar or settings nav entry | `runtime.navigation` |
| an env key, its type or its default | the manifest's `environment` |
| a UI string in either language | the manifest's `locales` |
| a job kind or its retry budget | `runtime.jobs` |
| a persona or a visual surface | `personas` / `runtime.visual` |

Then `go run ./cmd/ggg registry build && make generate`.

## `ggg sync` fails with "payload … sha256 mismatch"

You edited a file that a manifest owns, in the registry's own tree. The
manifest records a digest per payload, so the source and its declaration have
drifted. This is the authoring step, not a corruption:

```sh
go run ./cmd/ggg registry build     # re-reads every payload, rewrites the digests
go run ./cmd/ggg sync --offline
```

Run the two together. Between them another process writing the same file
re-opens the same gap, which is the usual cause of a mismatch naming a file
you did not touch.

## `ggg update` exited 4

Not an error: safe modules updated and at least one conflict is staged
because you had edited a file upstream also changed. Your bytes were not
touched. `ggg diff --upstream` names the diff files under
`tmp/ggg/conflicts/`; read one, then `ggg resolve KIND/NAME --path PATH` with
`--accept-upstream`, `--keep-local` or `--merged`. `sync --check` keeps
failing until you do, on purpose: a staged conflict lives in ignored `tmp/`,
so it must never be committable as a green state. See
[Extending](/docs/extending).

## `ggg doctor` reports `candidate_missing`

You cloned a repository whose lock carries conflict metadata but whose
ignored `tmp/` is empty. Rerun `ggg update` at the lock's target
`registry_commit`; it re-downloads, re-verifies and re-materializes the
candidates without touching your source, and `ggg resolve` then works
normally.

## Safari drops cookies in local dev

Two separate Safari behaviors: `Secure` cookies are ignored over http, and
`__Host-`-prefixed cookies without `Secure` are rejected outright. This is
why development uses the non-Secure `csrf_token` cookie. The fix is never to
weaken production — it's to not run `APP_ENV=production` on plain-http
localhost.

## Visual test diffs on macOS

Baselines are font-rendering-sensitive and pinned to the **Playwright Linux
container**; a Mac will always diff slightly. Never regenerate baselines
outside it. `make visual` compares inside the pinned image and is read-only —
run it first, because it reproduces CI's required `visual` job exactly.
`make visual-update` is the only command allowed to overwrite a committed
screenshot; follow it with `make visual` to prove the new baselines compare
cleanly. Rendered dates come from the frozen `TEST_NOW` clock, so if a diff
shows a date, your run is missing `APP_ENV=test`/`TEST_NOW`. If a diff shows
a Clerk user button or portal that CI never renders, your environment
supplied a real `CLERK_PUBLISHABLE_KEY`: the harness blanks the `CLERK_*`
keys precisely so a developer's `.env` cannot change the pixels. See
[Testing](/docs/testing).

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
`ui.NavSwap` carries it for every nav link (`templates.NavSwap` and
`web.NavSwap` are aliases of that one constant). (`scroll:window:top` reads
like the same thing but is a no-op in 4.0.0-beta6.)

## Clicking an in-page anchor flashes the wrong section

The link got boosted. htmx fetches the page, repaints `#content` at the top of
the document, and only then scrolls to the fragment. Links whose `href` contains
a `#` must stay unboosted (`isAnchorLink` in `nav.templ`) — the browser then
scrolls natively with no request at all.

## `make check` fails with "design-system violation"

`internal/web/templates/designsystem_test.go` reads every `*.templ` in the
`templates` package and refuses
raw hex, `dark:` variants, palette ramps (`text-red-600`), numeric brand steps
(`bg-brand-600`), `!` utility overrides, arbitrary lengths (`text-[10px]`) and
templ expressions inside quoted attributes. The failure names the file, the
line, the offending text and the fix.

There are **no exemptions** on purpose: the previous "templates use only
tokens" claim was documented for months while false in eighty-odd places, and
an exemption list is how that happens. So the fix is always to move the value
into the layer that owns it — a token in `@theme`, a class in
`@layer components`, or a renderer in `internal/web/templates/ui/`. See
[Frontend → Design system](/docs/frontend).

Watch for the trap that a `dark:` or `!` in *prose* (a comment, or documentation
copy rendered by a template) also matches: the scanner reads file text, which is
what makes the rule cheap and absolute. Reword the prose.

## An element renders with a literal `{ someFunc(x) }` as its class

templ does **not** interpolate inside a quoted attribute. `class="badge { f(x) }"`
emits the expression verbatim, so the element renders unstyled and nothing warns
you. Write `class={ "badge", f(x) }` (or `attr={ expr }` for any other
attribute). Rule 7 of the design-system test catches this now.

## 429s during load tests

Working as intended: **100 req/min with burst 200 per IP**, 429 responses
carry `Retry-After: 1`. `/static/*`, `/healthz`, and `/ingest/*` are exempt.
From a single load-test host you will trip it — spread client IPs, point the
generator at exempt paths, or take the 429s as proof the limiter works. And
remember it's per-process: two machines double the effective limit until you
swap in a shared store (see [Deployment](/docs/deployment)).
