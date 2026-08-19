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
- **Billing (Polar.sh)** — checkout, customer portal, webhook sync, entitlements, dunning + trial emails, merchant-of-record tax.
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
- **GDPR self-serve** — one-click JSON data export and email-confirmed account deletion with org-cascade safety checks at `/app/settings/account`.
- **Maintenance mode** — `MAINTENANCE_MODE=true` serves a dedicated 503 page (JSON for `/api/`) while `/healthz`/`/readyz` stay live; dedicated 403 page for non-admins.
- **Mobile nav** — hamburger drawer in the app topbar; the sidebar nav is shared, not duplicated.
- **Public API** — org-scoped Bearer tokens (`ggg_…`), `/api/v1/projects`, JSON error shape; OpenAPI 3.1 contract at `/api/v1/openapi.yaml`, kept in step with the router by tests.
- **Content** — markdown blog + RSS + sitemap + OG; docs section rendered in-app (`/docs`) with ranked search (`/docs/search`).
- **Platform** — Postgres job queue + schedules admin, audit log (configurable retention), rate limiting, strict CSP, CSRF, structured logs, Sentry + PostHog hooks, `/healthz` + `/readyz` + `/metrics` (Prometheus, bearer-gated).
- **Testing** — unit + integration (per-package test DBs) + seam contract suites + fuzz tests for trust-boundary parsers + race detector + Playwright e2e + docker-pinned visual baselines + a11y (axe) + smoke and docker-build CI jobs.

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

## Deployment

`docker build -t gogogadget .` — multi-stage build, distroless runtime,
everything embedded. `fly.toml` included; run migrations-on-boot against Neon.
See [/docs/deployment](content/docs/deployment.md).

## License

MIT — fork freely.
