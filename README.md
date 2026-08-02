# GoGoGadget

The production-grade **Go + HTMX** SaaS boilerplate. Marketing site, auth with
social login + 2FA, teams with roles + invitations, subscription billing,
transactional email, admin dashboard, markdown blog + docs, audit log, public
API, background jobs — wired to managed services so you own the product, not
the plumbing. One static binary; no node anywhere in production.

```
Browser ──▶ Go (net/http) ──▶ Postgres
              │  templ + HTMX + Alpine (CSP build)
              │  ├─ Clerk    (auth, orgs, 2FA — hosted portal + mirror sync)
              │  ├─ Polar.sh (billing, merchant of record)
              │  ├─ Resend   (email; DevSender locally → tmp/emails/)
              │  ├─ PostHog  (analytics, env-gated, /ingest proxy)
              │  └─ Sentry   (errors, env-gated)
              └─ jobs table (SKIP LOCKED worker)
```

## Features

- **Auth & teams (Clerk)** — social OAuth, 2FA, orgs, roles, invitations, all hosted; local mirror synced via webhooks for fast queries.
- **Billing (Polar.sh)** — checkout, customer portal, webhook sync, entitlements, dunning + trial emails, merchant-of-record tax.
- **App shell** — dashboard, projects CRUD (the canonical example), activity feed, settings (account/org/billing/API).
- **Admin** — users/orgs/MRR stats, user search + disable, plan badges.
- **Public API** — org-scoped Bearer tokens (`ggg_…`), `/api/v1/projects`, JSON error shape.
- **Content** — markdown blog + RSS + sitemap + OG; 20-page docs section rendered in-app (`/docs`).
- **Platform** — Postgres job queue, audit log, rate limiting, strict CSP, CSRF, structured logs, Sentry + PostHog hooks, `/healthz` + `/readyz`.
- **Testing** — unit + integration (per-package test DBs) + Playwright e2e + docker-pinned visual baselines + a11y (axe).

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
| Clerk | `CLERK_SECRET_KEY`, `CLERK_PUBLISHABLE_KEY`, `CLERK_WEBHOOK_SECRET`, `CLERK_PORTAL_URL` | Hosted sign-in/up, OAuth, 2FA, org invitations. Point the webhook at `/webhooks/clerk`. |
| Polar.sh | `POLAR_ACCESS_TOKEN`, `POLAR_WEBHOOK_SECRET`, `POLAR_PRODUCT_PRO`, `POLAR_PRODUCT_TEAM` | Checkout + portal for Pro/Team. `polar listen http://localhost:8080/webhooks/polar` locally. |
| Resend | `RESEND_API_KEY`, `EMAIL_FROM` | Real email delivery (otherwise DevSender writes `tmp/emails/`). |
| PostHog | `POSTHOG_API_KEY`, `POSTHOG_HOST` | Analytics via the consent-gated `/ingest` proxy. |
| Sentry | `SENTRY_DSN` | Panic + job dead-letter reporting. |

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
