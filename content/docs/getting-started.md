---
title: Getting started
description: From clone to running SaaS in ten minutes — with zero SaaS accounts.
section: Start
weight: 2
---

Prerequisites: **Go 1.26+** and **Docker** (for the local Postgres). Nothing
else — no node, no npm.

```sh
make setup
docker compose up -d db
make seed
make dev
```

Open http://localhost:8080.

## What each command does

- `make setup` checks your Go and Docker versions, runs `go mod download`,
  downloads the pinned Tailwind standalone binary to `bin/tailwindcss`
  (`scripts/setup-tools.sh`, sha256-verified), vendors the frontend assets
  (htmx 4, Alpine.js CSP build, clerk-js, Inter — `scripts/vendor-frontend.sh`,
  sha256-verified), and copies `.env.example` to `.env`.
- `docker compose up -d db` starts Postgres 16 (with a healthcheck) on port
  5432. Port already taken? Set `DB_PORT` in `.env` — see
  [Configuration](/docs/configuration).
- `make seed` loads `internal/db/testdata/seed_dev.sql`: a demo user
  (`demo@gogogadget.dev`), a demo org (`Demo Org`), and four projects.
- `make dev` is the one-terminal loop: templ watch + Tailwind watch + air,
  which rebuilds and restarts the server on every save. Browser refresh stays
  manual — the strict CSP forbids air's injected reload snippet.

## Zero-account mode

The fresh clone runs the **full app with zero SaaS accounts**. `.env.example`
ships `DEV_AUTH_BYPASS=true`, which swaps Clerk's verifier for a local one that
accepts synthetic session cookies of the shape `e2e:<userID>:<orgID>:<role>`.

Go straight to http://localhost:8080/dev/login — it sets the demo session
cookie and lands you in `/app` as `demo@gogogadget.dev`, admin of Demo Org,
with the four seeded projects on the dashboard. (Hitting `/login` or `/signup`
in this mode redirects to `/dev/login` too.)

`DEV_AUTH_BYPASS` is honored only outside production — booting with
`APP_ENV=production` and the bypass on is a hard startup error. Every guard
and middleware still executes in bypass mode, so tests and e2e exercise the
real request path.

## Connecting real services

When you are ready for real auth, billing, and email, create the accounts
listed in the README and fill in `.env`:

- **Clerk** — the four `CLERK_*` keys. See [Authentication](/docs/authentication).
- **Polar.sh** — `POLAR_ACCESS_TOKEN`, webhook secret, product IDs. See
  [Billing](/docs/billing).
- **Resend** — `RESEND_API_KEY` + `EMAIL_FROM`. Without it, the dev sender
  logs mail and writes rendered HTML to `tmp/emails/`. See [Email](/docs/email).

Every unconfigured service degrades to a 503 "not configured" fragment or a
log no-op — never a crash. All keys and defaults: [Configuration](/docs/configuration).

## The day-to-day loop

```sh
make check   # generate + vet + test + build — THE one-command gate
```

`make check` regenerates templ/sqlc/Tailwind output first, then vets, tests,
and builds. Run it before every commit. Other targets you will use daily:

```sh
make test       # unit + integration (integration self-skips without a test DB)
make e2e        # Playwright end-to-end suite
make db-reset   # destroy and recreate the local database, reseed demo data
```

`make db-reset` is the fix for a wedged local database: `docker compose down -v`,
fresh `up -d db`, then `cmd/seed -reset` — drop/create the database named in
`DATABASE_URL`, apply migrations, load the demo fixture.

Next: [Architecture](/docs/architecture) for the package map and request
lifecycle, or [Extending](/docs/extending) for the recipe hub.
