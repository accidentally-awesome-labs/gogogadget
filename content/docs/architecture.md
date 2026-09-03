---
title: Architecture
description: Manifests, the resolver, the generated bootstrap, provider slots, and the request lifecycle.
section: Core
weight: 3
---

A GoGoGadget project is one Go module that builds to one binary. All behavior
lives in `internal/` packages; `cmd/` holds entry points only. What makes it a
framework rather than a template is the layering below: **manifests own files
and declarations, the registry resolves one graph, the generated bootstrap
wires typed capabilities, provider slots select adapters per environment, and
seams keep vendor SDKs out of handlers.**

## Five layers

### 1. Manifests own files and declarations

One module is one directory: `registry/modules/<kind>/<name>/module.json` next
to the source it claims. The manifest is data only — no hook, no postinstall,
no command array — and it declares:

- **`files`** — exclusive ownership of each installed path plus its SHA-256.
  Two modules may never claim one path.
- **`requires`** — `{id, contract: {min, max}}`, an inclusive contract range.
  A dependency outside its range refuses before any payload is read.
- **`runtime`** — the typed contributions: `routes`, `jobs`, `janitors`,
  `navigation`, `slots`, `ui`, `scenarios`, `queries`, `content_types`,
  `assets`, `visual`, `personas`, `system`, `provider_slots`, `provisioners`,
  `database_ops`, `deploy`, `cli`.
- **`environment`**, **`locales`**, **`migrations`**, **`data`**,
  **`dependencies`** (`go`, `tools`, `containers`), **`claims`**,
  **`removal_policy`**.

Everything cross-cutting is *rendered* from those declarations, never hand
edited: the route table, the config struct, i18n catalogs, the OpenAPI
document, `.env.example`, the seed order, the Playwright surface inventory,
`compose.yaml`, and the three generated reference pages. `IsGeneratedOutputPath`
in `internal/modkit` is the list, and the planner refuses to let a module claim
a path that matches it.

### 2. The registry resolves one graph

`gogogadget.json` is intent — schema 2:

```json
{
  "schema": 2,
  "registries": [{ "namespace": "ggg", "source": "directory", "path": "registry" }],
  "modules": ["ggg/profile/full"],
  "exclude": ["ggg/component/table-empty"],
  "providers": { "ggg/mail": { "development": { "adapter": "ggg/system/mail-dev", "target": "filesystem" }, "…": {} } },
  "deployment": "ggg/system/deploy-fly"
}
```

Ids are globally scoped `<namespace>/<kind>/<name>`, so several registries can
be configured at once with no shadowing and no fork of the core catalog. A
remote (`github`) registry is pinned by ref and a base64 Ed25519 public key,
and its signed snapshot plus every payload digest is verified before the
catalog is parsed.

Resolution runs in one pass and is pure — it writes nothing:

1. Resolve explicitly selected modules and profiles into a base closure.
2. Derive the exact set of provider slots that closure declares.
   `providers` must name that set exactly, with a `{adapter, target}` for
   `development`, `test` and `production`. Selected adapters enter the graph
   with reason `provider`; the deployment module with reason `deployment`.
3. Refuse, before any byte is written: a missing dependency, an out-of-range
   contract, a duplicate claim, a cycle, an unknown slot, a wrong-slot adapter,
   an unknown target, a target not allowed in that environment, and a
   development-mode target selected for production.

`gogogadget.lock.json` is the resolved counterpart: the registry provenance
ledger (namespace, requested ref, canonical module, key fingerprint), one
snapshot record per referenced registry commit, the deterministic install
`order`, one runtime order per environment, the provider selections, the
managed Go dependency ledger, and per module a manifest snapshot with a
per-file `base_sha256` / `local_sha256` / `state`.

### 3. Apply is one journalled transaction

Every mutating command previews a `Plan` and applies it through a journal that
snapshots the pre-run bytes **and modes** of every path it will touch,
including generated outputs and `go.mod`/`go.sum`. Any failure restores them
and exits 5. A refusal (exit 3) means nothing was written at all.

### 4. The generated bootstrap wires typed capabilities

`internal/modules/bootstrap_registry_gen.go` is rendered from the lock. It
declares one `Runtime` field per capability (`AnalyticsCapturer`,
`MailSender`, `StorageStore`, …), boots the non-replaceable `config` system
first, then switches on `Config.Env` into `bootDevelopment`, `bootTest` or
`bootProduction` — each generated from that environment's runtime order. An
unknown `APP_ENV` fails before any provider constructor runs.

All three environments' adapters compile into the one binary; only the
matching branch initializes. Executable contributions an adapter owns —
routes, jobs, janitors, navigation, slots, shell assets — are gated by a
generated `providerActive(env, slot, adapter)` predicate, so an inactive
adapter never registers anything. Migrations and seed data are the installed
union and run everywhere: one artifact has one schema.

Lifecycle and health are registrations, not conventions. A system that
declares `stop` appends an `apphost.Stop`; one that declares `health` must
satisfy `apphost.HealthChecker`, and the generator emits a compile-time
assignment proving it. `Runtime.Health(ctx)` aggregates every registered check
concurrently with a 2-second deadline, recovers a panicking check as
unhealthy, caches the report for 10 seconds, and reports `Ready` false only
when a **critical** slot's check fails.

### 5. Provider slots select adapters per environment

A **provider slot** is a constructor-free seam module that declares the
capabilities supplied together — `ggg/identity` supplies
`identity.verifier`, `identity.fetcher`, `identity.deleter`,
`identity.navigator` and `identity.webhook`. An **adapter** is a system module
that implements one slot and advertises its selectable **service targets**;
a target carries its mode (`development`, `self-hosted`, `managed`), the
environments it is allowed in, an automation level, declared inputs, and — for
a local one — the container, ports, volumes and health check that generate its
compose service.

Three slots are `critical: true`, so only their outages can make a runtime
report unready: `ggg/database`, `ggg/identity`, `ggg/billing`.

| Layer | Rule |
|---|---|
| Seam package (`internal/mail`) | Interfaces, value types, contract tests. No vendor SDK, no vendor env key. |
| Adapter package (`internal/mail/resend`) | The one place a vendor SDK is imported. Owns its own env declarations, dependencies, lifecycle, health, and targets. |
| Handlers | Receive capabilities. Never import an SDK, never construct a fallback. |

Thirteen of the sixteen seam packages hold to that literally — their only
non-stdlib imports are `templ` (mail renderers) and the project's own
packages. Three do not, and the reasons differ: `internal/telemetry` exports
OpenTelemetry's `TracerProvider` and `MeterProvider` *as* its capability
types, so OTel is the contract rather than a vendor detail; `internal/identity`
and `internal/billing` still carry the Clerk SDK, `svix` and
`standard-webhooks` for their webhook parsers, which have not moved into the
adapters yet — see [Roadmap](/docs/roadmap). Handlers are clean in every case:
`internal/web` imports no provider SDK at all.

`web.NewModule` receives non-nil capabilities from the generated boot wiring
and refuses a missing required one rather than quietly building a dev
implementation. That is why an unconfigured managed selection is one joined
boot error instead of a silent local fallback.

## Package map

| Path | Owns |
|---|---|
| `cmd/server` | `apphost.OS` → `modules.Boot` → run → `Runtime.Close` |
| `cmd/ggg` | The CLI entry point; a thin shell over `internal/gggcli` |
| `cmd/seed` | Loads module-owned fixtures; `-reset`, `-registry dev\|e2e` |
| `internal/modkit` | Registry engine: catalog, resolver, planner, apply transaction, generators |
| `internal/gggcli` | Command table, controller, renderers; `internal/gggcli/ui` is the Charm console |
| `internal/modules` | Generated boot/lifecycle/health DAG |
| `internal/apphost` | The leaf `Host` seam plus health aggregation |
| `internal/remote` | Provisioner, deploy-target and database-operator contracts; `.ggg/env` and `.ggg/state.json` |
| `internal/config` | Generic env/dotenv reading; the typed struct and its validation are generated |
| `internal/db` | pgx pool, embedded goose migrations, sqlc queries, per-package test DBs |
| `internal/web` | HTTP surface: middleware chain, HTMX helpers, handlers, templ templates |
| `internal/<seam>` | One provider seam each: identity, billing, mail, storage, cache, ratelimit, search, realtime, notifications, webhooks, usage, analytics, observability, telemetry, llm, flags |
| `internal/jobs` | Postgres worker (SKIP LOCKED), scheduler pass, delivery, usage flush, exports |
| `internal/audit`, `internal/content`, `internal/i18n`, `internal/api` | Audit ledger, embedded markdown, locale detection, Bearer JSON transport |

## Request lifecycle

The chain is assembled in `Server.Handler()` and the order is load-bearing:

```
request
└─ MaxBytesReader (10 MB cap, every route)
   └─ provider environment (APP_ENV into the template context)
      └─ telemetry.HTTP   span + duration attributes
         └─ routeBodyLimit   narrows to RoutePolicy.MaxBodyBytes where a route declares a tighter cap
            └─ requestID   16-byte hex id, exposed as X-Request-Id
               └─ accessLog   one slog line per request (5xx at ERROR)
                  └─ i18n.Detect   ?lang= → ggg_lang cookie → Accept-Language → en
                     └─ maintenanceMode   MAINTENANCE_MODE=true → 503 (JSON under /api/); probes + /static/ exempt
                        └─ rateLimit   per-IP token bucket
                           └─ secureHeaders   strict CSP, nosniff, frame/permission policies, HSTS in prod
                              └─ sessionLoad   verify the session cookie (optional; absent → unauthenticated)
                                 └─ csrf (nosurf)   exempt: /webhooks/* /api/* /ingest/* /healthz /readyz /static/*
                                    └─ routes
```

Inside routing, three guarded groups wrap their own chains:

| Group | Chain |
|---|---|
| `/app/*` | `requireAuth` → `requireNotDisabled` → `requireOrg` → `loadPlan` |
| `/admin/*` | the `/app` chain + `requireStaff` → `requireAdminWrite` |
| `/api/v1/*` | `RequireAPIToken(scope)` — cookieless Bearer, so CSRF-exempt |

Routes are **declared, not registered**: a `runtime.routes` entry plus a
`claims.routes` id, and the mux is built from
`internal/web/routes_registry_gen.go`. Editing `internal/web/routes.go` does
nothing.

The app is **stateless** — session claims travel in a cookie, the job queue
lives in Postgres — so horizontal scaling is a machine count. The one
node-local default is the memory rate limiter, and swapping it is a provider
selection (`ggg/rate-limit` → `rate-limit-redis`), not a code change.

## Context keys

Identity and plan state travel through `context.Context` under keys defined in
`internal/identity/context.go`. Handlers always read through the accessors.

| Key | Value | Accessor | Set by |
|---|---|---|---|
| `ctxUser` | local `users` row | `UserFrom(ctx)` | `sessionLoad` |
| `ctxClaims` | internal claims (`UserID`, `OrgID`, `OrgRole`, `OrgSlug`) | `ClaimsFrom(ctx)` | `sessionLoad` |
| `ctxOrg` | local `orgs` row for the active org | `OrgFrom(ctx)` | `sessionLoad` (when claims carry an org) |
| `ctxPlan` | `billing.Plan` | `PlanFrom(ctx)` (defaults to free) | `loadPlan` |
| `ctxSub` | `*sqlc.Subscription` (nil = free) | `SubFrom(ctx)` | `loadPlan` |

Claims carry **internal** ids. A provider's own subject never reaches domain
code: `identity_subjects` and `identity_organizations` map
`(provider, subject)` to `usr_…` / `org_…`, and `billing_accounts` maps
`(provider, provider_customer_id)` to an org. Swapping an identity provider
therefore does not rewrite a single domain row. See [Security](/docs/security).

## go:embed strategy

The binary ships everything: `static/` (CSS, JS, fonts, vendored assets),
`content/` (blog + docs markdown), and `internal/db/migrations/` are embedded
with `go:embed`. The deployable artifact is one file, so there is nothing to
sync, mount, or drift between deploys. Migrations run on boot from the
embedded FS under goose's advisory lock.

## The generated-code rule

Two families are generated and **never edited by hand**:

- **Registry-owned**, rendered by `ggg sync`: every `*_registry_gen.*`,
  `compose.yaml`, `compose.test.yaml`, `static/ui-components.js`,
  `static/ui-engines.js`, `.env.example`, `e2e/generated/*.ts`,
  `internal/web/templates/scenarios_gen.go`,
  `internal/web/templates/ui/reference_gen.go`, and the
  [configuration](/docs/configuration-reference),
  [module](/docs/module-reference) and
  [component](/docs/component-reference) reference pages.
- **External-tool output**: `*_templ.go` (templ), `internal/db/sqlc/` (sqlc),
  `static/app.css` (Tailwind).

Edit the declaration or the source and run `ggg generate` (also the first step
of `ggg check`). If a generated file looks wrong, the declaration or the
generator is wrong — fix it there.
