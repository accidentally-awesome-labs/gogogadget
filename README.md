# GoGoGadget

The production-grade **Go + HTMX** SaaS boilerplate. Marketing site, auth with
social login + 2FA, teams with roles + invitations, subscription billing,
transactional email, admin dashboard, markdown blog + docs, audit log, public
API, background jobs — wired to managed services so you own the product, not
the plumbing. One static binary; no node anywhere in production.

```
Browser ──▶ Go (net/http) ──▶ Postgres
              │  templ + htmx 4 + Alpine (CSP build)
              │  ├─ Clerk    (auth, orgs, 2FA — hosted portal + mirror sync)
              │  ├─ Polar.sh (billing, merchant of record)
              │  ├─ Resend   (email; DevSender locally → tmp/emails/)
              │  ├─ PostHog  (analytics, env-gated, /ingest proxy)
              │  ├─ R2       (file storage; DevStore locally → tmp/uploads/)
              │  ├─ LLM      (any OpenAI-compatible API, forced model, metered)
              │  └─ Sentry   (errors, env-gated)
              └─ jobs table (SKIP LOCKED worker)
```

## Features

- **Auth & teams (Clerk)** — social OAuth, 2FA, orgs, roles, invitations, all hosted; local mirror synced via webhooks for fast queries.
- **Billing (Polar.sh)** — checkout, customer portal, webhook sync, entitlements, dunning + trial emails, merchant-of-record tax. Dunning is a scheduled day-0/+3/+7 sequence whose follow-ups re-check the subscription, so a recovered customer is never chased.
- **App shell** — dashboard, projects CRUD (the canonical example), activity feed, settings (account/org/billing/API/webhooks).
- **i18n** — en + es with ?lang=/cookie/Accept-Language detection, `i18n.T` in every template, zero-dependency catalogs (`x/text`), and a CSP-safe switcher.
- **File storage** — `storage.Store` seam: Cloudflare R2 (presigned downloads) or zero-account DevStore; org-scoped rows, plan quotas, attachment-only downloads.
- **In-app notifications** — per-user rows, sidebar bell + unread badge, SSE stream (with heartbeat) that survives boosted navigation.
- **Outbound webhooks** — customer endpoints with standard-webhooks signatures, secret rotation with a 24h dual-sign grace window, job-queue retries + dead-letter + replay, and a double-checked SSRF guard.
- **Usage metering** — local `usage_events` + a scheduled flush to Polar events (idempotent, `ue-<id>` dedup); plan meters render on the billing page.
- **AI seam** — `llm.Completer` over any OpenAI-compatible API; forced server-side model; metered `/api/v1/ai/chat` (402 at the plan cap).
- **Feature flags** — DB-backed, per-org overrides, deterministic FNV rollouts; admin CRUD + override UI at `/admin/flags`.
- **Admin impersonation** — audited "view as" sessions with a 2-hour expiry, banner, and safe mid-session invalidation.
- **Search** — Postgres FTS (generated tsvector + GIN) with an ILIKE fallback; no vendor.
- **Frontend (htmx 4)** — boosted navigation scoped to one content box with View Transitions, morph swaps that keep table rows' DOM nodes, and server-driven `HX-Location` navigation that never rebuilds the shell (so third-party widgets never flash). No bundler, no hydration.
- **Admin** — users/orgs/MRR stats, user search + disable, plan badges, platform-wide audit-log viewer (`/admin/audit`), jobs viewer with dead-letter requeue (`/admin/jobs`), and announcement banner CRUD (`/admin/announcements`).
- **Announcement banner** — one-active-at-a-time platform announcements (info/warning/critical) rendered in the app shell, dismissible per user, 30s-cached.
- **Notification preferences** — per-user, per-kind in-app mutes at `/app/settings/notifications`; default-on, absent row means send.
- **Email digest** — per-user cadence (off/daily/weekly) rolling up in-app notifications; worker-rendered, window-stamped so nothing repeats or is skipped.
- **Appearance preferences** — theme and language stored per user, mirrored to cookies; dark mode paints server-side (no flash on a new device).
- **Staff roles** — read-only `support` tier beside full `admin`; one method-based guard covers every mutating admin route, grants are audited, last admin can't be demoted.
- **SEO** — self-referential canonicals that collapse the `?lang=` duplicates, reciprocal hreflang, Organization/WebSite/BlogPosting JSON-LD, sitemap `lastmod`, RSS autodiscovery.
- **GDPR self-serve** — one-click JSON data export and email-confirmed account deletion with org-cascade safety checks at `/app/settings/account`.
- **Org data export** — `org:admin` JSON bundle (members, projects, files, audit, billing, API/webhook inventory) delivered as a background job; secrets excluded by construction.
- **Maintenance mode** — `MAINTENANCE_MODE=true` serves a dedicated 503 page (JSON for `/api/`) while `/healthz`/`/readyz` stay live; dedicated 403 page for non-admins.
- **Mobile nav** — hamburger drawer in the app topbar; the sidebar nav is shared, not duplicated.
- **Public API** — org-scoped Bearer tokens (`ggg_…`), `/api/v1/projects`, JSON error shape; OpenAPI 3.1 contract at `/api/v1/openapi.yaml`, kept in step with the router by tests Cursor pagination, idempotency keys on POST, per-token rate limits.
- **Content** — markdown blog + RSS + sitemap + OG; docs section rendered in-app (`/docs`) with ranked search (`/docs/search`), plus a `/changelog` collection rendered newest-first with per-release anchors.
- **Platform** — Postgres job queue + schedules admin, audit log (configurable retention), rate limiting, strict CSP, CSRF, structured logs, Sentry + PostHog hooks, `/healthz` + `/readyz` + `/metrics` (Prometheus, bearer-gated).
- **Testing** — unit + integration (per-package test DBs) + seam contract suites + fuzz tests for trust-boundary parsers + race detector + Playwright e2e + docker-pinned visual baselines + a11y (axe) + smoke and docker-build CI jobs.
- **Module registry + `ggg` CLI** — the whole product is 240 installable source modules (elements, components, pages, workflows, systems). `ggg add`/`update`/`remove`/`diff`/`sync` install real source you own and edit; update never overwrites a local edit, it stages the upstream candidate and reports a conflict. Declared intent in `gogogadget.json`, resolved truth in a committed lock, and every cross-cutting registry (routes, jobs, env, i18n, OpenAPI, queries, nav, static, seeds) generated from the manifests.
- **Typed UI catalog** — 172 templ renderers in `internal/web/templates/ui`, one options struct each (`templ Badge(o BadgeOpts)`), closed enums instead of stringly variants, and `Attrs` with no arbitrary-attribute escape hatch so a caller cannot override a component's own `role`/`aria-*`. Live reference at `/dev/gallery`, twelve realistic product scenarios at `/dev/scenarios`, and both feed the generated visual + axe matrices.

## Quick start

Prerequisites: **Go 1.26+**, **Docker**.

```sh
make setup                      # tools, vendored assets, .env
docker compose up -d db
make seed                       # demo user/org/projects
make dev                        # http://localhost:8080
```

A fresh clone runs the **full app with zero SaaS accounts** —
`.env.example` ships `DEV_AUTH_BYPASS=true`. Click **Sign in** (uses the dev
login) and you land in the seeded demo org.

The day-to-day gate is one command: `make check` (generate + vet + test + build).

## Modules and the `ggg` CLI

Everything in this repository is an installable module with one owner, a
declared contract, and a stated cost of removal. `ggg` is the CLI that resolves,
installs, inspects, and removes them. It ships source — files you own and edit —
not a dependency you call.

```sh
go run ./cmd/ggg catalog --kind component     # what exists, and its state
go run ./cmd/ggg info component/data-table    # contract, files, links, verify commands
go run ./cmd/ggg add component/data-table     # install it and its dependency closure
go run ./cmd/ggg diff                         # what have I changed since install?
go run ./cmd/ggg update                       # advance; never overwrites a local edit
go run ./cmd/ggg sync --check --offline       # does the tree match the lock?
```

Intent lives in `gogogadget.json`; resolved truth lives in the committed
`gogogadget.lock.json`, which records a per-file digest ledger — that is what
lets update tell your bytes from upstream's and refuse to clobber them. Every
command speaks `--json` with a fixed envelope and branchable exit codes, so an
agent can drive it without scraping output.

- [CLI and registry](content/docs/cli.md) — every command, flag, envelope key, exit code
- [Module anatomy and lifecycle](content/docs/modules.md) — manifests, closure resolution, the lock
- [Module removal and data retention](content/docs/module-removal.md) — policies and migration guarantees
- [Module reference](content/docs/module-reference.md) — all 240 modules, generated
- [UI foundations](content/docs/ui-foundations.md) — three layers, one options struct, closed enums
- [Component usage](content/docs/components.md) — finding a component, `native_fallback`, progressive enhancement
- [Component reference](content/docs/component-reference.md) — all 172 signatures, generated
- [Gallery and scenarios](content/docs/gallery.md) — `/dev/gallery`, `/dev/scenarios`, the generated test matrices

## Connect real services

Fill in `.env` (every key is documented in `.env.example` and
[/docs/configuration](content/docs/configuration.md)):

| Service | Keys | What you get |
|---|---|---|
| Clerk | `CLERK_SECRET_KEY`, `CLERK_PUBLISHABLE_KEY`, `CLERK_WEBHOOK_SECRET`, `CLERK_PORTAL_URL` | Hosted sign-in/up, OAuth, 2FA, org invitations. Point the webhook at `/webhooks/clerk`; in **Account Portal → Redirects**, set both fallback sign-in and sign-up URLs to `{APP_URL}/?after-auth=1`. |
| Polar.sh | `POLAR_ACCESS_TOKEN`, `POLAR_WEBHOOK_SECRET`, `POLAR_PRODUCT_PRO`, `POLAR_PRODUCT_TEAM` | Checkout + portal for Pro/Team. `polar listen http://localhost:8080/webhooks/polar` locally. |
| Resend | `RESEND_API_KEY`, `EMAIL_FROM` | Real email delivery (otherwise DevSender writes `tmp/emails/`). |
| Cloudflare R2 | `STORAGE_R2_ACCOUNT_ID`, `STORAGE_R2_ACCESS_KEY_ID`, `STORAGE_R2_SECRET_ACCESS_KEY`, `STORAGE_R2_BUCKET` | File uploads/downloads (otherwise DevStore writes `tmp/uploads/`). `STORAGE_R2_ENDPOINT` redirects at S3/MinIO. |
| LLM (any OpenAI-compatible API) | `LLM_API_KEY`, `LLM_MODEL` (optional `LLM_BASE_URL`) | Metered `/api/v1/ai/chat` (OpenAI, OpenRouter, Vercel AI Gateway, Ollama, Groq). |
| PostHog | `POSTHOG_API_KEY`, `POSTHOG_HOST` | Analytics via the consent-gated `/ingest` proxy. |
| Sentry | `SENTRY_DSN` | Panic + job dead-letter reporting (via the `observability.Reporter` seam). |

New Go dependencies added with this stack: `github.com/aws/aws-sdk-go-v2`
(config/credentials/s3 — the R2 store is the only SDK-touching file) and
`golang.org/x/text` (now direct, for i18n catalogs). The archived
`polarsource/polar-go` SDK was removed in favor of raw `net/http` behind
`billing.Client`.

Everything unconfigured degrades to a 503 "not configured" fragment or a log
no-op — never a crash.

## Documentation

The full docs ship **in the app** at `/docs` (and in `content/docs/`):
[getting started](content/docs/getting-started.md) ·
[architecture](content/docs/architecture.md) ·
[configuration](content/docs/configuration.md) ·
[authentication](content/docs/authentication.md) ·
[billing](content/docs/billing.md) ·
[testing](content/docs/testing.md) ·
[deployment](content/docs/deployment.md) ·
[extending](content/docs/extending.md) (the recipe hub) ·
[troubleshooting](content/docs/troubleshooting.md)

The **Modules** section documents the stable interfaces of the registry and the
UI catalog:
[CLI and registry](content/docs/cli.md) ·
[module anatomy and lifecycle](content/docs/modules.md) ·
[module removal and data retention](content/docs/module-removal.md) ·
[UI foundations](content/docs/ui-foundations.md) ·
[component usage](content/docs/components.md) ·
[gallery and scenarios](content/docs/gallery.md) ·
[module reference](content/docs/module-reference.md) ·
[component reference](content/docs/component-reference.md)

Four of those pages are generated from the module manifests —
`configuration-reference`, `module-reference`, `component-reference`, and the
`.env.example` they share their source with — so an inventory of 240 modules and
172 components cannot drift from what is installed. Do not edit them; edit the
declaring module's manifest and run `make generate`.

## Deployment

`docker build -t gogogadget .` — multi-stage build, distroless runtime,
everything embedded. `fly.toml` included; run migrations-on-boot against Neon.
See [/docs/deployment](content/docs/deployment.md).

## License

MIT — fork freely.
