---
title: Deployment
description: One static binary, the Dockerfile, compose, fly.io, Neon, and the scaling caveats.
section: Guides
weight: 16
---

The whole app ships as **one static binary**: templates, static assets,
migrations, blog, and these docs are embedded with `go:embed`. There is no
runtime dependency on the repo checkout — the binary plus a Postgres URL is
the deployable unit.

## The Dockerfile, stage by stage

`make docker-build` builds `gogogadget:local` locally. The image is
multi-stage so the final artifact carries no toolchain:

| Stage | Base | What happens |
|---|---|---|
| tools | curl image | Downloads the Tailwind standalone binary (`linux-x64`), sha256-verified like `scripts/setup-tools.sh` |
| build | `golang:1.26-alpine` | `go mod download` → `go tool templ generate` + `go tool sqlc generate` → `bin/tailwindcss -i input.css -o static/app.css --minify` → `CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=$VERSION" ./cmd/server` |
| final | `gcr.io/distroless/static-debian12:nonroot` | The binary only. `EXPOSE 8080` |

- Generated code is produced **inside** the image build, so the shipped CSS
  and sqlc output can never drift from their sources.
- The `-X main.version` stamp surfaces on `GET /healthz` as
  `{"status":"ok","version":"…"}` — deploy provenance for free.
- Distroless `nonroot`: no shell, no package manager, runs unprivileged.
  Everything the app serves (templates, `static/`, `content/`, migrations)
  was embedded at build time, so nothing else needs to be in the image.

## compose: the full stack locally

Alongside the `db` service, the `app` service builds from the repo root:

```yaml
app:
  build: .
  env_file: .env
  depends_on:
    db:
      condition: service_healthy
  ports:
    - "8080:8080"
```

```sh
docker compose up --build
```

Migrations run on boot (below), so the first `up` migrates the fresh database
automatically. `make smoke` then exercises the running stack.

## fly.io

`fly.toml` sets `internal_port = 8080`, `[env] APP_ENV = "production"`, and a
minimum of one machine. First deploy:

```sh
fly launch          # detects the Dockerfile, adopts fly.toml
fly secrets set \
  APP_URL=https://your-app.fly.dev \
  DATABASE_URL='postgres://…neon.tech/gogogadget?sslmode=require' \
  CLERK_SECRET_KEY=sk_live_… \
  CLERK_WEBHOOK_SECRET=whsec_… \
  CLERK_PORTAL_URL=https://accounts.your-domain.com \
  CLERK_PUBLISHABLE_KEY=pk_live_…
fly deploy
```

Every key **required in production** must be present — the boot refuses to
start without them, so a misconfigured deploy fails loudly at boot, not on
the first request (see [Configuration](/docs/configuration)). Then the
feature keys:

```sh
fly secrets set POLAR_ACCESS_TOKEN=… POLAR_WEBHOOK_SECRET=… \
  POLAR_PRODUCT_PRO=… POLAR_PRODUCT_TEAM=… POLAR_SERVER=production
fly secrets set RESEND_API_KEY=… EMAIL_FROM='You <hello@your-domain.com>'
fly secrets set ADMIN_EMAIL=you@your-domain.com
fly secrets set POSTHOG_API_KEY=… SENTRY_DSN=…   # optional, env-gated
```

Scale out later with `fly scale count=2` — but read the caveats below first.

## Neon (serverless Postgres)

1. Create a Neon project and database.
2. Copy the pooled connection string into `DATABASE_URL`.
3. Keep `sslmode=require` — Neon speaks TLS. The local default
   (`sslmode=disable`) is for the compose database only.

`DATABASE_URL` is required in production: the boot errors without it rather
than silently falling back to the localhost default. No "deployed against my
laptop" incidents.

## Environment checklist

| Key | Production | Notes |
|---|---|---|
| `APP_ENV` | `production` | Gates HSTS, JSON logs, pprof, draft content |
| `APP_URL` | required | Absolute origin; feeds redirects and emails |
| `PORT` | `8080` | Matches `internal_port` |
| `DATABASE_URL` | **required** | Neon string with `sslmode=require` |
| `CLERK_SECRET_KEY` | **required** | All four `CLERK_*` keys together |
| `CLERK_WEBHOOK_SECRET` | **required** | From the Clerk webhook endpoint |
| `CLERK_PORTAL_URL` | **required** | Account Portal base URL |
| `CLERK_PUBLISHABLE_KEY` | **required** | Drives the vendored clerk-js refresh |
| `CLERK_FRONTEND_API_URL` | derived | Defaults to `https://clerk.<APP_URL host>` |
| `POLAR_*` | optional | Billing routes 503 "not configured" without them |
| `RESEND_API_KEY` / `EMAIL_FROM` | optional | Without: DevSender writes `tmp/emails/` |
| `ADMIN_EMAIL` | optional | Grants the admin flag on first sign-in |
| `POSTHOG_API_KEY` / `POSTHOG_HOST` | optional | Analytics off without a key |
| `SENTRY_DSN` | optional | Error tracking off without it |
| `DEV_AUTH_BYPASS` | **must stay off** | `true` with `APP_ENV=production` is a boot error |

## /healthz vs /readyz

| Endpoint | Semantics | DB | Use for |
|---|---|---|---|
| `GET /healthz` | Liveness: process is up; returns the build version | never touched | Probes that must not flap on a DB blip |
| `GET /readyz` | Readiness: 200 only when a DB ping succeeds, else 503 | ping | Platform healthchecks (fly, compose) |

A database hiccup must never restart the container (liveness), but it must
stop traffic (readiness). Point the platform's healthcheck at `/readyz`.

## Migrations on boot

`db.Migrate` runs the embedded goose migrations on every boot — before the
server accepts traffic. It is idempotent, and goose's **advisory lock** means
N instances booting against the same database migrate exactly once while the
rest wait. There is no separate migrate step to forget in the pipeline; roll
forward by adding a migration file and deploying. See
[Database](/docs/database).

## Backups

Neon's point-in-time recovery covers the production database — set the
retention window in the Neon console and a restore is a dashboard operation.
Locally the "backup" is `make db-reset`: destroy and recreate the dev
database from `internal/db/testdata/seed_dev.sql`.

## Scaling caveats

The app is **stateless** — Clerk JWT sessions, a Postgres-backed job queue —
so horizontal scaling is `fly scale count=N`. Two things to know first:

- **The rate limiter is the one node-local component.** Limits are enforced
  per process, so two machines double the effective per-IP limit. Scaling
  past one machine is the documented trigger to swap the in-process limiter
  for a shared store (e.g. Upstash). See [Security](/docs/security).
- **The job worker is already multi-node safe.** Claims use
  `FOR UPDATE SKIP LOCKED` with a 5-minute visibility timeout, so every
  instance can run its embedded worker against the same table without
  double-processing. See [Background jobs](/docs/background-jobs).
