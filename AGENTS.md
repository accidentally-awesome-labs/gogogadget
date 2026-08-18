# AGENTS.md — operating manual for coding agents

GoGoGadget is a Go + HTMX + Alpine.js + templ + Postgres SaaS boilerplate.
Read this before changing anything. The docs site in `/docs` (content/docs/)
is the deep reference and updates in the same change as any behavior edit.

## Repo map

- `cmd/server` — wiring only: config → db → migrator → services → web.Server → graceful shutdown. `cmd/seed` — fixture loader with `-reset`.
- `internal/config` — env parsing + validation (stdlib only; `.env` dev auto-load).
- `internal/db` — pgx pool, embedded goose migrations (`migrations/`), sqlc queries (`queries/` → `sqlc/`), `testdata/` seeds, `testdb/` per-package test DBs.
- `internal/web` — HTTP surface: middleware chain, HTMX helpers, handlers, templ templates (`templates/`).
- `internal/identity` — `Verifier` seam (Clerk is the ONLY SDK file), context keys, guards data, Clerk webhook sync parsers, `UserFetcher`.
- `internal/billing` — plan truth (`plans.go`), `Client` seam, Polar client (raw net/http — the archived SDK is gone), webhook state machine, `Entitled`/`CurrentPlan`.
- `internal/jobs` — Postgres worker (SKIP LOCKED, 5-min visibility timeout, 2^n backoff, dead-letter) + the scheduler pass (recurring `schedules`) + webhook delivery + usage flush + CSV export.
- `internal/mail` — `Sender` seam (Resend / DevSender→tmp/emails), templ email renderers.
- `internal/audit` — fire-and-forget audit log.
- `internal/content` — embedded markdown (blog + docs), goldmark, RSS.
- `internal/analytics` — PostHog `Capturer` seam + `/ingest` proxy.
- `internal/api` — Bearer token auth + `/api/v1` JSON (second transport, same rules).
- `internal/i18n` — locale detection middleware, `T()` lookup, en+es catalogs.
- `internal/storage` — `Store` seam (R2 / DevStore→tmp/uploads); R2 file is the ONLY aws-sdk import.
- `internal/notify` — fire-and-forget in-app notifications.
- `internal/webhooks` — outbound webhook emit + secret minting (deliveries run in jobs).
- `internal/usage` — fire-and-forget metering; flushed to Polar by `usage.flush`.
- `internal/llm` — `Completer` seam (OpenAI-compatible net/http client).
- `internal/flags` — DB-backed feature flags (30s cache, FNV bucketing, per-org overrides).
- `internal/schedules` — builder-facing recurring-work helper.
- `internal/observability` — `Reporter` seam (SentryReporter / NoopReporter); the ONLY internal sentry-go import.
- `static/` — built CSS (generated), `app.js` (all client logic), vendored JS/fonts. `content/` — markdown. `e2e/` — Playwright (node lives ONLY here).

## Golden commands

- `make dev` — one-terminal loop (templ watch + tailwind watch + air).
- `make check` — THE gate: generate + vet + test + build. Run before every commit.
- `make seed` / `make db-reset` — demo data / nuke local db.
- `make e2e` — Playwright suite (needs `docker compose up -d db`).
- `make visual-update` — regenerate visual baselines in the pinned Linux container (ONLY way; macOS screenshots diff by design).

## Run without accounts

Fresh clone works end-to-end: `.env.example` ships `DEV_AUTH_BYPASS=true`;
`make setup && docker compose up -d db && make seed && make dev` →
`/dev/login` signs in as the demo user. No Clerk/Polar/Resend account needed.
E2E auth shape: cookie `__session=e2e:<userID>:<orgID>:<role>` (empty org = no
active org). `DEV_AUTH_BYPASS` is boot-refused when `APP_ENV=production`.

## Conventions (verbatim, load-bearing)

- **Runtime is htmx 4** (`static/vendor/htmx.min.js`, sha256-pinned in `scripts/vendor-frontend.sh`). Attribute inheritance is EXPLICIT (`hx-headers:inherited` on `<body>` for CSRF); config lives in the `<meta name="htmx-config">` tag, not JS.
- **Fragment rule** (`wantsFragment` in `internal/web/htmx.go`, in order): history-restore → full page; `HX-Target` is `#content` → full page (replacing the content box IS a navigation); else `HX-Request-Type: full`→page / `partial`→fragment; no such header (pre-4.0) → fragment unless `HX-Boosted`.
- **Navigation swaps ONLY `#content`** (`hx-boost` + `hx-target/hx-select="#content"` + `hx-swap="outerHTML transition:true show:top"`). The shell must never be a swap target — clerk-js portals live on `<body>`. `ContentTarget` is the single constant.
- **Chrome rule**: pages reachable by a boosted link MUST render identical chrome around `#content`; anything layout-specific goes INSIDE it (docs TOC lives in `docsBody`, not the shell). `PublicLayout`/`DocsLayout` share `publicShell`; `AdminLayout` *is* `AppLayout`. Guarded by `TestPublicAndDocsLayoutsShareChrome`.
- **Anchor links (`#…`) are never boosted** (`isAnchorLink` in `nav.templ`) — boosting repaints `#content` at the top before scrolling, flashing the wrong section.
- **Redirects**: `Navigate(w, r, url)` = soft, in-app (`HX-Location` scoped to `#content`, no reload, clerk stays mounted) — pair with `Toast`. `Redirect(w, r, url)` = hard (`HX-Redirect`/303) for another origin or an auth/layout change — pair with `FlashToast`.
- **htmx statuses**: 422 = re-rendered form fragment (htmx 4's `noSwap` default `[204,304]` already swaps 422/503); row deletes `hx-delete` + `hx-confirm` + `closest tr` + `outerHTML` + 200 empty; forms carry `novalidate` + `hx-disable="this"` (htmx 4's rename of `hx-disabled-elt`) + a `Spinner()`.
- **Swap methods**: `innerMorph` for table search/pagination and `outerMorph` for the billing poll — morph matches by `id`, so table rows carry stable ids. Search inputs add `hx-sync="this:replace"` + `hx-indicator`.
- **XSS rule**: templ auto-escapes; user-controlled content NEVER goes through `templ.Raw`. goldmark without `html.WithUnsafe`.
- **Modularity seams**: handlers never import SDKs — `identity.Verifier`, `billing.Client`, `mail.Sender`, `analytics.Capturer`. Swapping a provider = replacing one file.
- **No new dependencies** without a manifest entry (see README stack table).
- **One query file per table** in `internal/db/queries/`; every UPDATE sets `updated_at = now()`.
- **`data-testid`** on every element a test asserts on.
- **Generated files are NEVER edited**: `*_templ.go`, `internal/db/sqlc/`, `static/app.css`. Edit sources, run `make generate`.
- **Design system**: semantic tokens + component classes (`.btn`, `.input`, `.card`, `.badge`, `.table`, `.link`, `.alert-*`, `.prose`) — no ad-hoc hex colors or pixel values in templates; rebrand via `@theme` in `input.css`.
- **No inline scripts** anywhere (CSP `script-src 'self'`); client logic lives in `static/app.js` as `Alpine.data` components.
- **Middleware order is load-bearing**: recover → requestID → accessLog → i18n.Detect → maintenanceMode → rateLimit → secureHeaders → sessionLoad → csrf → route groups (appChain: RequireAuth → RequireNotDisabled → RequireOrg → LoadPlan → [RequireAdmin]; /api: RequireAPIToken).
- **UI strings go through `i18n.T(ctx, "area.key")`** in templates — catalogs in `internal/i18n/catalog_*.go`, en+es; content markdown and handler-side strings (titles, toasts) stay English (documented in `/docs/i18n`).
- **Webhook header families**: Clerk = `svix-*` (svix lib), Polar = `webhook-*` (standard-webhooks lib). Fixtures mirror real names. Outbound customer webhooks also sign with the standard-webhooks lib (`webhook-id`/`webhook-timestamp`/`webhook-signature`); the SSRF guard is double-checked (URL validate + dial-time IP allowlist) and https-only in every environment.
- **Fire-and-forget quartet**: `audit.Log`, `notify.Send`, `webhooks.Emit`, `usage.Record` — errors are logged, never returned; a notification/webhook/meter hiccup must never fail the caller.
- **SSE notes**: the notifications stream disables the 30s WriteTimeout per-response via `http.NewResponseController(w).SetWriteDeadline(time.Time{})` — that only works because middleware `statusWriter` implements `Unwrap()`; keep it. The `sse-connect` div lives in the shell so boosted nav never kills the stream; badge fragment swaps target the bubble, not the shell.

## Test-layer decision rule

Pure logic → unit. Handler/route behavior → integration (`TEST_DATABASE_URL`,
per-package DBs via `internal/db/testdb`). User flow → e2e spec. Pixel-level →
visual spec (docker baselines only).

## Definition of done

`make check` green + new behavior covered at the layer from the rule above.

## Task playbook

Recipes (add CRUD resource / plan / email kind / job kind / webhook event /
API endpoint / swap providers): **`/docs/extending`**
(`content/docs/extending.md`).

## Docs discipline

Any user-facing behavior change updates the matching `content/docs/` page in
the same change.
