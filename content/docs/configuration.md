---
title: Configuration
description: Every environment variable, what requires it, and what happens when it is missing.
section: Core
weight: 4
---

All configuration is environment variables, parsed and validated by
`internal/config/config.go` (`Load()`). Stdlib only — no env library. All
validation problems are reported together at boot, never one at a time.

`.env` is auto-loaded in **development only**, via a tiny inline parser that
never overrides variables already set in the real environment. In test and
production the process environment is the only source.

## The full table

| Key | Required | Default | Notes |
|---|---|---|---|
| `APP_ENV` | | `development` | `development` \| `test` \| `production` |
| `APP_URL` | | `http://localhost:8080` | Public base URL; trailing slash trimmed. Feeds auth/checkout redirects |
| `PORT` | | `8080` | Must parse as 1–65535 |
| `DATABASE_URL` | **production** | `postgres://postgres:postgres@localhost:5432/gogogadget?sslmode=disable` | Dev default matches `docker compose up -d db`. No fallback in production — boot refuses to guess |
| `CLERK_SECRET_KEY` | **production** | | Empty → auth not configured; `/app` renders 503 (unless dev bypass, below) |
| `CLERK_WEBHOOK_SECRET` | **production** | | Verifies `svix-*` signatures on `/webhooks/clerk` |
| `CLERK_PORTAL_URL` | **production** | | Hosted Account Portal base, e.g. `https://accounts.your-app.com` |
| `CLERK_PUBLISHABLE_KEY` | **production** | | Drives the vendored clerk-js that keeps the `__session` JWT fresh |
| `CLERK_FRONTEND_API_URL` | | dev: `https://*.clerk.accounts.dev`; prod: `https://clerk.<APP_URL host>` | Clerk Frontend API origin — feeds CSP `connect-src` |
| `ADMIN_EMAIL` | | | First sign-in with this email is granted site admin. Empty → nobody is admin |
| `POLAR_ACCESS_TOKEN` | | | Empty → billing routes render 503 "not configured" |
| `POLAR_WEBHOOK_SECRET` | | | Verifies `webhook-*` signatures on `/webhooks/polar` |
| `POLAR_PRODUCT_PRO` | | | Polar product ID for the Pro plan |
| `POLAR_PRODUCT_TEAM` | | | Polar product ID for the Team plan |
| `POLAR_SERVER` | | `sandbox` | `sandbox` \| `production` — anything else is a boot error |
| `RESEND_API_KEY` | | | Empty → DevSender: logs mail + writes rendered HTML to `tmp/emails/` |
| `EMAIL_FROM` | | `GoGoGadget <hello@example.com>` | |
| `POSTHOG_API_KEY` | | | Empty → disabled: no client script, no `/ingest` proxy, server capture no-ops |
| `POSTHOG_HOST` | | `https://us.i.posthog.com` | Target of the `/ingest` reverse proxy |
| `SENTRY_DSN` | | | Empty → disabled |
| `TEST_NOW` | | | RFC3339. Freezes the render clock — honored **only** when `APP_ENV=test` (visual-test determinism) |
| `DEV_AUTH_BYPASS` | | `false` | Enables synthetic `e2e:` session tokens. `true` + `APP_ENV=production` is a hard boot error |
| `LOG_LEVEL` | | `debug` in development, `info` otherwise | `debug` \| `info` \| `warn` \| `error` |

Two more variables matter but are **not** read by `config.Load()`:

| Key | Read by | Default | Notes |
|---|---|---|---|
| `DB_PORT` | `compose.yaml` | `5432` | Host port for the local Postgres. Set it (and match the port in `DATABASE_URL`) when 5432 is taken |
| `TEST_DATABASE_URL` | `internal/db/testdb` | `postgres://postgres:postgres@localhost:5432/gogogadget_test?sslmode=disable` | Server used by integration tests; each package gets its own database. See [Database](/docs/database) |

## Production validation rules

With `APP_ENV=production`, boot fails unless all of these hold:

- `DATABASE_URL` is set explicitly (no dev fallback DSN).
- All four `CLERK_*` keys are set: `CLERK_SECRET_KEY`, `CLERK_WEBHOOK_SECRET`,
  `CLERK_PORTAL_URL`, `CLERK_PUBLISHABLE_KEY`.
- `DEV_AUTH_BYPASS` is not `true` (refused outright).

Validated in every environment: `APP_ENV` must be one of the three values,
`PORT` must be a valid port, `POLAR_SERVER` must be `sandbox` or `production`,
and a malformed `TEST_NOW` under `APP_ENV=test` is an error.

## Degradation, not crashes

Everything optional degrades cleanly when unset: Clerk → `/app` 503s (or dev
bypass mode), Polar → billing routes 503, Resend → DevSender, PostHog and
Sentry → no-ops. You can adopt services in any order.

## Recipes

**Local development, zero accounts** (what `.env.example` ships):

```sh
APP_ENV=development
DEV_AUTH_BYPASS=true
DATABASE_URL=postgres://postgres:postgres@localhost:5432/gogogadget?sslmode=disable
```

**Local development with real Clerk:** add the four `CLERK_*` keys. When Clerk
is configured, `/login` goes to the hosted portal instead of `/dev/login`.

**Tests** (what `e2e/playwright.config.ts` uses):

```sh
APP_ENV=test PORT=18080 DEV_AUTH_BYPASS=true \
DATABASE_URL=postgres://postgres:postgres@localhost:5432/gogogadget_e2e \
TEST_NOW=2026-01-15T00:00:00Z
```

**Production:** `APP_ENV=production`, a real `DATABASE_URL` (e.g. Neon), the
four `CLERK_*` keys, `CLERK_PORTAL_URL`, plus whatever optional services you
use. `LOG_LEVEL` defaults to `info` and logs go out as JSON. See
[Deployment](/docs/deployment).
