---
title: Architecture
description: Package map, request lifecycle, context keys, and the rules that keep the codebase small.
section: Core
weight: 3
---

GoGoGadget is a single Go module (`github.com/gogogadget/gogogadget`) that
builds to one binary. All behavior lives in `internal/` packages; `cmd/` holds
entry points only.

## Package map

| Path | Owns |
|---|---|
| `cmd/server` | Wiring only: config → db → migrator → services → `web.Server` → graceful shutdown |
| `cmd/seed` | Loads a SQL fixture; `-reset` drops/recreates the database first |
| `internal/config` | Env parsing + validation, stdlib only — no env library |
| `internal/db` | pgx pool (`Open`), embedded goose migrations, `queries/`, generated `sqlc/`, `testdb` |
| `internal/web` | HTTP server: routes, middleware, HTMX helpers, handlers, templ templates |
| `internal/identity` | `Verifier` seam (Clerk + fake), context keys, `Require*` guards, Clerk webhook sync |
| `internal/billing` | Plan truth (`plans.go`), `Client` seam (Polar), webhook state machine, entitlements |
| `internal/jobs` | Postgres-backed job worker (`FOR UPDATE SKIP LOCKED`) |
| `internal/mail` | `Sender` seam: Resend in production, file-writing dev sender locally |
| `internal/audit` | `audit.Log` — fire-and-forget org activity |
| `internal/content` | Markdown collections (blog + docs), goldmark rendering, RSS |
| `internal/analytics` | PostHog wrapper (no-op without key) + `/ingest` reverse proxy |
| `internal/api` | Bearer-token middleware + versioned JSON API (`/api/v1`) |

**The modularity rule:** every external service hides behind one narrow
interface — `identity.Verifier`, `billing.Client`, `mail.Sender`,
`analytics.Capturer` — and handlers never import an SDK. Swapping a provider
means replacing one file; new cross-cutting needs follow the same seam
pattern.

## Request lifecycle

The middleware chain is assembled in `Server.Handler()` and the order is
load-bearing:

```
request
└─ MaxBytesReader (10 MB cap, every route)
   └─ recover        panics → 500 page (+ Sentry when SENTRY_DSN is set)
      └─ routeBodyLimit   narrows to RoutePolicy.MaxBodyBytes where a route declares a tighter cap
      └─ requestID   16-byte hex id, exposed as X-Request-Id
         └─ accessLog   one slog line per request (5xx at ERROR)
            └─ i18n.Detect   locale: ?lang= → ggg_lang cookie → Accept-Language → en
               └─ maintenanceMode   MAINTENANCE_MODE=true → 503 page (JSON under /api/); probes + /static/ exempt
                  └─ rateLimit   100 req/min per IP, burst 200 (single-node, in-process)
                  └─ secureHeaders   strict CSP, nosniff, frame/permission policies, HSTS in prod
                  └─ sessionLoad   verify Clerk __session cookie (optional; absent → unauthenticated)
                     └─ csrf (nosurf)   exempt: /webhooks/* /api/* /ingest/* /healthz /readyz /static/*
                        └─ routes
```

Inside routing, three guarded groups wrap their own chains:

| Group | Chain |
|---|---|
| `/app/*` | `RequireAuth` → `RequireNotDisabled` → `RequireOrg` → `LoadPlan` |
| `/admin/*` | the `/app` chain + `RequireAdmin` |
| `/api/v1/*` | `RequireAPIToken(scope)` — cookieless Bearer, so CSRF-exempt |

The app is **stateless**: Clerk JWTs carry auth (no server-side session) and
the job queue lives in Postgres, so horizontal scaling is `fly scale count=N`.
The one node-local component is the rate limiter — swapping it to a shared
store is the documented upgrade trigger (see [Security](/docs/security)).

## Context keys

Identity and plan state travel through `context.Context` under keys defined in
`internal/identity/context.go`. Handlers always read through the accessors —
never touch the keys directly.

| Key | Value | Accessor | Set by |
|---|---|---|---|
| `ctxUser` | local `users` mirror row | `UserFrom(ctx)` | `sessionLoad` |
| `ctxClaims` | session claims (user/org/role IDs) | `ClaimsFrom(ctx)` | `sessionLoad` |
| `ctxOrg` | local `orgs` row for the active org | `OrgFrom(ctx)` | `sessionLoad` (when claims carry an org) |
| `ctxPlan` | `billing.Plan` | `PlanFrom(ctx)` (defaults to free) | `LoadPlan` |
| `ctxSub` | `*sqlc.Subscription` (nil = free) | `SubFrom(ctx)` | `LoadPlan` |

## go:embed strategy

The binary ships everything: `static/` (CSS, JS, fonts, vendored assets),
`content/` (blog + docs markdown), and `internal/db/migrations/` are all
embedded with `go:embed`. The deployable artifact is one file — the binary —
so there is nothing to sync, mount, or drift between deploys. Migrations run
on boot from the embedded FS.

## The generated-code rule

Three trees are generated and **never edited by hand**:

- `*_templ.go` — from `.templ` sources (templ)
- `internal/db/sqlc/*.go` — from `internal/db/queries/*.sql` (sqlc)
- `static/app.css` — from `input.css` (Tailwind)

Edit the sources and run `make generate` (also the first step of
`make check`). If a generated file looks wrong, the source or the generator
config is wrong — fix it there.
