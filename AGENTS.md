# AGENTS.md — operating manual for coding agents

GoGoGadget is a Go + HTMX + Alpine.js + templ + Postgres SaaS boilerplate,
distributed as a source module registry driven by the `ggg` CLI.
Read this before changing anything. The docs site in `/docs` (content/docs/)
is the deep reference and updates in the same change as any behavior edit.

## Source vs generated — read this first

Every cross-cutting file is RENDERED from module manifests by `ggg sync`. A
hand edit there survives until the next `make generate`, then vanishes
silently. Generated (never edit): `*_templ.go`, every `*_registry_gen.*`,
`internal/db/sqlc/`, `static/app.css`, `static/ui-components.js`,
`static/ui-engines.js`, `.env.example`,
`content/docs/configuration-reference.md`,
`content/docs/module-reference.md`, `content/docs/component-reference.md`,
`e2e/generated/*.ts`, `internal/web/templates/scenarios_gen.go`,
`internal/web/templates/ui/reference_gen.go`. That list is
`modkit.IsGeneratedOutputPath`, not a convention.

Declarations live in `registry/modules/<kind>/<name>/module.json`: `files`
(exclusive ownership + sha256), `requires`, `runtime.{routes,jobs,navigation,
ui,slots,scenarios,queries,content_types,assets,visual}`, `environment`,
`locales`, `migrations`, `data`, `removal_policy`. Everything else —
handlers, templates, queries, `input.css` — is ordinary editable source.

A `class:"test"` payload may also set `self_host: true`. That marks an
assertion about THIS repository — the committed snapshot signature, the
`registry/testdata` and `registry/external-testdata` fixtures,
`templates/external-registry`, `registry/schema`, `.github/workflows`, the
vendored bytes, the git-index ownership sweep — and the installer skips it in
any project whose `go.mod` module path is not the registry's
`canonical_module`. So a new self-hosting test goes in a `self_host` payload
(the `*_selfhost_test.go` files, or `ci_workflow_test.go`,
`e2e_ownership_test.go`, `fuzz_gate_test.go`, `registry_build_internal_test.go`,
`external_template_test.go`); anything portable stays in a normal test payload
so generated projects keep running it. NEVER reach for `t.Skip` when an
artifact is absent: that lets the core gate pass by skipping.

## The loop

```sh
go run ./cmd/ggg info KIND/NAME       # owner, files, gallery/route links, verify commands
go run ./cmd/ggg catalog [--kind K]   # every module and its state (240 selected here)
go run ./cmd/ggg add KIND/NAME        # edits gogogadget.json, then reconciles
go run ./cmd/ggg diff                 # every file whose bytes differ from the lock
make generate                         # ggg sync --offline → templ → sqlc → tailwind
```

After editing a file a manifest owns, run
`go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline` **as one
command** — build refreshes payload digests, and a sync without it fails with
`sha256 mismatch`. `update` never overwrites locally modified source: it
stages the upstream candidate under `tmp/ggg/conflicts/` and exits 4 for
`ggg resolve`. Exit codes: 0 ok, 1 runtime, 2 usage, 3 refusal, 4 conflict,
5 rolled back. Full workflows: **[/docs/extending](content/docs/extending.md)**.

## Repo map

- `cmd/ggg` — the module CLI (thin shell over `internal/modkit`). `cmd/server` — `apphost.OS` → `modules.Boot` → run → `Runtime.Close`; all wiring is generated. `cmd/seed` — `-reset`, `-registry dev|e2e`.
- `internal/modkit` — registry engine: catalog, planner, apply transaction, generators. `internal/modules` — generated boot/lifecycle DAG. `internal/apphost` — the leaf `Host` seam.
- `internal/config` — generic env/dotenv reading; the typed struct, its validation, its production refusals and its cross-key derivations are all generated from manifest `environment` records. Authored code here NEVER reads an adapter-declared field: a module that does not declare a key uses `cfg.Value("KEY")`/`cfg.BoolValue("KEY")`, so removing the adapter removes the field without breaking the reader.
- `internal/db` — pgx pool, embedded goose migrations (`migrations/`, immutable and forward-only), sqlc queries (`queries/` → `sqlc/`), `testdata/seed/{dev,e2e}/` module-owned fixtures, `testdb/` per-package test DBs (`TEST_DB_SUFFIX` for concurrent workers).
- `internal/web` — HTTP surface: middleware chain, HTMX helpers, handlers, templ templates (`templates/`).
- `internal/identity` — the provider-neutral seam: ports, `Claims`/`ProviderClaims`, `Event`, context keys, guards data. It imports NO SDK. `identity/clerk` is the only clerk-sdk-go + svix importer (session verify, user fetch, delete, portal URLs, `svix-*` verification, Clerk payload parsing) and also owns the 55 vendored clerk-js payloads and `internal/identity/clerkurl`, the leaf that derives `CLERK_FRONTEND_API_URL` for the generated config loader; `identity/devadapter` is the zero-account adapter (`e2e:` tokens, derived profiles, unsigned envelope); `identity/session` maps verified subjects onto opaque ids; `identity/contract` is the one table both adapters run.
- `internal/billing` — plan truth (`plans.go`), `Client`/`PlanCatalog`/`BillingWebhook` seams, webhook state machine, `Entitled`/`CurrentPlan`, and `billing/contract` (the shared table). It imports no provider library: `billing/polar` owns the raw net/http Polar client and `standard-webhooks` verification, `internal/billinglocal` the zero-account one.
- `internal/jobs` — Postgres worker (SKIP LOCKED, 5-min visibility timeout, 2^n backoff, dead-letter) + the scheduler pass (recurring `schedules`) + webhook delivery + usage flush + CSV export.
- `internal/mail` — `Sender` seam (Resend / DevSender→tmp/emails), templ email renderers.
- `internal/audit` — fire-and-forget audit log.
- `internal/content` — embedded markdown (blog + docs), goldmark, RSS.
- `internal/analytics` — PostHog `Capturer` seam + `/ingest` proxy.
- `internal/api` — Bearer token auth + `/api/v1` JSON (second transport, same rules).
- `internal/i18n` — locale detection middleware, `T()` lookup; the en+es catalogs are GENERATED from manifest `locales` blocks.
- `internal/storage` — `Store` seam (R2 / DevStore→tmp/uploads); R2 file is the ONLY aws-sdk import.
- `internal/notify` — fire-and-forget in-app notifications.
- `internal/webhooks` — outbound webhook emit + secret minting (deliveries run in jobs).
- `internal/usage` — fire-and-forget metering; flushed to Polar by `usage.flush`.
- `internal/llm` — `Completer` seam (OpenAI-compatible net/http client).
- `internal/flags` — DB-backed feature flags (30s cache, FNV bucketing, per-org overrides).
- `internal/schedules` — builder-facing recurring-work helper.
- `internal/observability` — `Reporter` seam (SentryReporter / NoopReporter); the ONLY internal sentry-go import.
- `static/` — built CSS (generated), `app.js` (shell only: theme, nav, SSE, toasts, clerk boot), `ui/*.js` (module-owned Alpine components), vendored JS/fonts. `content/` — markdown. `e2e/` — Playwright (node lives ONLY here).

## Golden commands

- `make dev` — one-terminal loop (templ watch + tailwind watch + air).
- `make check` — THE gate: generate + `ggg sync --check --offline` + vet + test + build. Run before every commit.
- `make seed` / `make db-reset` — demo data / nuke local db.
- `make e2e` — Playwright suite (`ggg test e2e` brings the test stack up itself; the server it drives runs on the host at `:18080`).
- `make visual` — compare visual baselines in the pinned Linux container (what CI's required `visual` job runs). `make visual-update` — the ONLY thing allowed to overwrite a committed screenshot; macOS screenshots diff by design.

## Run without accounts

Fresh clone works end-to-end: `.env.example` ships `DEV_AUTH_BYPASS=true`;
`make setup && bin/ggg services up && make seed && make dev` →
`/dev/login` signs in as the demo user. No Clerk/Polar/Resend account needed.
E2E auth shape: cookie `__session=e2e:<userID>:<orgID>:<role>` (empty org = no
active org). `DEV_AUTH_BYPASS` is boot-refused when `APP_ENV=production`.

## Conventions (verbatim, load-bearing)

- **Runtime is htmx 4** (`static/vendor/htmx.min.js`, sha256-pinned in `scripts/vendor-frontend.sh` AND declared under `vendors` in `system/static`'s manifest, which `ggg registry build` re-verifies by byte count and digest). Attribute inheritance is EXPLICIT (`hx-headers:inherited` on `<body>` for CSRF); config lives in the `<meta name="htmx-config">` tag, not JS.
- **Fragment rule** (`wantsFragment` in `internal/web/htmx.go`, in order): history-restore → full page; `HX-Target` is `#content` → full page (replacing the content box IS a navigation); else `HX-Request-Type: full`→page / `partial`→fragment; no such header (pre-4.0) → fragment unless `HX-Boosted`.
- **Navigation swaps ONLY `#content`** (`hx-boost` + `hx-target/hx-select="#content"` + `hx-swap="outerHTML transition:true show:top"`). The shell must never be a swap target — clerk-js portals live on `<body>`. `ContentTarget` is the single constant.
- **Chrome rule**: pages reachable by a boosted link MUST render identical chrome around `#content`; anything layout-specific goes INSIDE it (docs TOC lives in `docsBody`, not the shell). `PublicLayout`/`DocsLayout` share `publicShell`; `AdminLayout` *is* `AppLayout`. Guarded by `TestPublicAndDocsLayoutsShareChrome`.
- **Anchor links (`#…`) are never boosted** (`isAnchorLink` in `nav.templ`) — boosting repaints `#content` at the top before scrolling, flashing the wrong section.
- **Redirects**: `Navigate(w, r, url)` = soft, in-app (`HX-Location` scoped to `#content`, no reload, clerk stays mounted) — pair with `Toast`. `Redirect(w, r, url)` = hard (`HX-Redirect`/303) for another origin or an auth/layout change — pair with `FlashToast`.
- **htmx statuses**: 422 = re-rendered form fragment (htmx 4's `noSwap` default `[204,304]` already swaps 422/503); row deletes are `hx-delete` + `closest tr` + `outerHTML` + 200 empty, and the confirmation is `ui.ConfirmAction` — NEVER `hx-confirm`, which calls `window.confirm` and so cannot be translated, styled or asserted; forms carry `novalidate` + `hx-disable="this"` (htmx 4's rename of `hx-disabled-elt`) + a `Spinner()`.
- **Swap methods**: `innerMorph` for table search/pagination and `outerMorph` for the billing poll — morph matches by `id`, so table rows carry stable ids. Search inputs add `hx-sync="this:replace"` + `hx-indicator`.
- **XSS rule**: templ auto-escapes; user-controlled content NEVER goes through `templ.Raw`. goldmark without `html.WithUnsafe`.
- **Modularity seams**: handlers never import SDKs — `identity.Verifier`, `billing.Client`, `mail.Sender`, `analytics.Capturer`. Swapping a provider = replacing one file.
- **No new dependencies** without a manifest entry (see README stack table).
- **One query file per table** in `internal/db/queries/`; every UPDATE sets `updated_at = now()`.
- **`data-testid`** on every element a test asserts on.
- **Routes and nav are DECLARED, not registered**: a `runtime.routes` entry (`id`, `method`, `pattern`, `scope`, `policy`, `package`, `handler`) plus a `claims.routes` id; the mux is built from `internal/web/routes_registry_gen.go`. Editing `internal/web/routes.go` does nothing.
- **Design system is three layers, one home each**: **tokens** (`input.css` `@theme` + `.dark`; `theme.go` for email, the one surface that inlines hex), **component classes** (`input.css` `@layer components` — `.btn`/`.btn-{primary,ghost,danger,inverse}` × `.btn-{sm,xs,lg,icon}`, `.input`, `.card`, `.table-card`, `.page-*`, `.nav-link`, `.tab`, `.prose`, plus the `.k-{brand,info,success,warn,danger,neutral}` matrix that feeds `--ui-solid|solid-fg|tint|tint-fg|line|text` to `.badge`/`.alert`/`.banner`/`.toast`), **templ components** (`internal/web/templates/ui/*.templ` + `icons.templ`). `designsystem_test.go` fails the `templates` package on a raw hex, `dark:` variant, palette ramp, numeric brand step, `!` override, arbitrary length or quoted-attribute interpolation.
- **One options struct per UI component**: every exported renderer in `package ui` is `templ Name(o NameOpts)` and every `NameOpts` embeds `ui.Attrs` (`ID`, `Class`, `TestID`, `Title`, `Decorative`, `Data`, CSP-safe `Alpine`, `HX`). `Attrs` has NO arbitrary-attribute map — components own `role`/`aria-*`/`tabindex`/`type`/base class and callers cannot override them. Enums are closed and normalize (empty or unknown → `KindNeutral`, `SizeMD`, `LiveOff`, …). `ui` imports templ + stdlib only, never `templates`/`billing`/`identity`/sqlc. Held by `ui/contract_test.go`; the 172 signatures live in `ui/reference_gen.go` and `ggg info`, never in prose.
- **No inline scripts** anywhere (CSP `script-src 'self'`). Shell logic is `Alpine.data` in `static/app.js`; component logic belongs to the owning module in `static/ui/*.js`, registered on `alpine:init` and loaded before `alpine-csp.min.js`.
- **Middleware order is load-bearing**: maxBytes → recover → routeBodyLimit → requestID → accessLog → i18n.Detect → maintenanceMode → rateLimit → secureHeaders → sessionLoad → csrf → route groups (app: `requireAuth → requireNotDisabled → requireOrg → loadPlan`; admin: that chain + `requireStaff → requireAdminWrite`; `/api`: `RequireAPIToken`). `routeBodyLimit` narrows the 10 MB global cap to `RoutePolicy.MaxBodyBytes`; it sits outside csrf because parsing a form reads the body. The policy matcher is fed the same `enabledRoutes` slice the mux is, so a route whose `Enabled` gate refused registration resolves to no policy at all.
- **UI strings go through `i18n.T(ctx, "area.key")`** in templates. The keys live in the owning module's manifest `locales` block, `{"en": …, "es": …}` — `internal/i18n/catalog_*_registry_gen.go` is generated from them, and generation refuses a duplicate owner, a key missing from a locale, or mismatched format placeholders. Content markdown and handler-side strings (titles, toasts) stay English (see `/docs/i18n`).
- **Webhook header families**: Clerk = `svix-*` (svix lib), Polar = `webhook-*` (standard-webhooks lib). Fixtures mirror real names. Outbound customer webhooks also sign with the standard-webhooks lib (`webhook-id`/`webhook-timestamp`/`webhook-signature`); the SSRF guard is double-checked (URL validate + dial-time IP allowlist) and https-only in every environment. `isPublicIP` is an allow-list — global unicast, then explicit non-routable blocks including RFC 6598 `100.64.0.0/10` — so an unenumerated range fails closed. Delivery refuses redirects outright (`CheckRedirect`): a 302 would hand a signed payload to a host the guard never classified, including back to `http://`.
- **Fire-and-forget quartet**: `audit.Log`, `notify.Send`, `webhooks.Emit`, `usage.Record` — errors are logged, never returned; a notification/webhook/meter hiccup must never fail the caller.
- **SSE notes**: the notifications stream disables the 30s WriteTimeout per-response via `http.NewResponseController(w).SetWriteDeadline(time.Time{})` — that only works because middleware `statusWriter` implements `Unwrap()`; keep it. The stream is a native `EventSource` in `app.js` (NOT htmx-ext-sse, which is htmx 2 only and threw under htmx 4); its carrier div lives in the shell so boosted nav never kills the connection, and badge fragment swaps target the bubble, not the shell.

## Test-layer decision rule

Pure logic → unit. Handler/route behavior → integration (`TEST_DATABASE_URL`,
per-package DBs via `internal/db/testdb`; set `TEST_DB_SUFFIX` per worker when
running the same package concurrently). User flow → e2e spec. Pixel-level →
visual spec (`make visual`; baselines only via `make visual-update`).
`go run ./cmd/ggg info KIND/NAME` prints the exact commands a module declares.

## Definition of done

`make check` green + new behavior covered at the layer from the rule above. If
you touched a manifest-owned file, `go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline` first.

## Task playbook

Recipes (add CRUD resource / plan / email kind / job kind / webhook event /
API endpoint / component / locale / content type / swap providers), plus
module authoring, `ggg diff`, conflict resolution, and the data-loss rules
(`.env` never edited, migrations retained and forward-only, `update` never
overwrites local source, stricter removal for API/identity, vendored widget
assets self-hosted and checksummed): **`/docs/extending`**
(`content/docs/extending.md`).

## Docs discipline

Any user-facing behavior change updates the matching `content/docs/` page in
the same change.
