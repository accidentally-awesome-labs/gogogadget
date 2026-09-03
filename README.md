# GoGoGadget

An opinionated **Go + templ + htmx 4 + Alpine + Postgres** application
framework, shipped as a source-module registry and driven by the `ggg` CLI.
Modules install as ordinary source files your project owns; infrastructure
hides behind provider slots you select per environment. One static binary, no
node in production.

The promise, in order:

1. **Choose a profile** — `minimal`, `web`, `api`, `saas`, or `full`.
2. **Choose one adapter and service target per required provider slot, per
   environment** — `mail-dev@filesystem` in development, `mail-resend@resend`
   in production.
3. **Preview** — every command plans first; nothing is written during planning.
4. **Apply** — one journalled transaction that rolls the tree back on failure.
5. **Own the source** — what lands is your code, editable with no fork.
6. **Keep updating without losing local edits** — `ggg update` never overwrites
   a file you changed; it stages the upstream candidate and exits 4.

```
Browser ──▶ Go (net/http) ──▶ Postgres
              │  templ + htmx 4 + Alpine (CSP build)
              │
              │  18 provider slots, one adapter+target per environment:
              │  identity · billing · mail · storage · database · cache
              │  rate-limit · search · realtime · feature-flags
              │  notifications · webhooks · usage · analytics
              │  observability · telemetry · audit-export · llm
              │
              └─ jobs table (SKIP LOCKED worker), schedules, audit ledger
```

Postgres is not a slot you swap for something else: sqlc, migrations,
transactional jobs, the audit ledger, notifications, schedules, usage,
webhooks, flags, content, and default search are all built on it. The
`ggg/database` slot chooses **where** Postgres runs (Docker locally, Neon in
production), not **whether** it is Postgres.

## Quick start

Prerequisites: **Go 1.26+**, **Docker**.

```sh
go build -o /tmp/ggg ./cmd/ggg
/tmp/ggg new ../my-app --module example.com/my-app --profile saas \
  --registry directory:. --deployment ggg/system/deploy-docker
```

`ggg new` writes `gogogadget.json` with your profile, every provider
selection and your deployment module, resolves the graph, and applies it in
one journalled transaction. Then, in the new project:

```sh
cd ../my-app
/tmp/ggg setup       # verified tools, go mod, generation — and it builds bin/ggg
bin/ggg services up  # the local services your selections actually need
bin/ggg db migrate
bin/ggg db seed
bin/ggg dev          # templ watch + tailwind watch + air, one process group
```

`setup` is the only step that needs an external `ggg`: it builds `bin/ggg`
from the project's own `cmd/ggg` source, and every later command rides that
binary.

To adopt a directory you already have instead of creating one, use
`ggg init` — with `--module` for a directory without a `go.mod`, or `--adopt`
to produce the initial lock from what is already installed.

**Zero SaaS accounts.** The development and test defaults are local adapters:
synthetic identity, in-app billing, filesystem mail and storage, Postgres
flags/search/realtime, a logging reporter, memory cache and rate limiting.
`.env.example` ships `DEV_AUTH_BYPASS=true`; `/dev/login` signs you in as the
seeded demo user. `DEV_AUTH_BYPASS=true` with `APP_ENV=production` is a hard
boot error, so the escape hatch cannot ship.

The day-to-day gate is one command: `ggg check` (generate → drift check → vet
→ test → build). `make check` is a thin alias for it.

## Provider slots

Every slot has a deterministic zero-account local option and one maintained
managed reference. 18 slots, 36 adapter modules, 42 selectable
adapter@target pairs.

| Slot | Development / test default | Production default | Other targets |
|---|---|---|---|
| `ggg/database` | `database-postgres@docker-postgres` | `database-postgres@neon` | — |
| `ggg/identity` | `identity-dev@local` | `identity-clerk@clerk` | — |
| `ggg/billing` | `billing-local@local` | `billing-polar@polar` | — |
| `ggg/mail` | `mail-dev@filesystem` | `mail-resend@resend` | `mail-smtp@mailpit`, `mail-smtp@smtp` |
| `ggg/storage` | `storage-filesystem@filesystem` | `storage-s3@r2` | `storage-s3@minio` |
| `ggg/cache` | `cache-memory@memory` | `cache-redis@upstash` | `cache-redis@valkey` |
| `ggg/rate-limit` | `rate-limit-memory@memory` | `rate-limit-redis@upstash` | `rate-limit-redis@valkey` |
| `ggg/search` | `search-postgres@postgres` | `search-postgres@postgres` | `search-typesense@typesense` |
| `ggg/realtime` | `realtime-postgres@postgres` | `realtime-postgres@postgres` | `realtime-ably@ably` |
| `ggg/feature-flags` | `feature-flags-postgres@postgres` | `feature-flags-postgres@postgres` | `feature-flags-launchdarkly@launchdarkly` |
| `ggg/notifications` | `notifications-postgres@postgres` | `notifications-postgres@postgres` | `notifications-knock@knock` |
| `ggg/webhooks` | `webhooks-postgres@postgres` | `webhooks-postgres@postgres` | `webhooks-svix@svix` |
| `ggg/usage` | `usage-postgres@postgres` | `usage-postgres@postgres` | `usage-openmeter@openmeter` |
| `ggg/analytics` | `analytics-noop@local` | `analytics-posthog@posthog` | — |
| `ggg/observability` | `observability-log@log` | `observability-log@log` | `observability-sentry@sentry` |
| `ggg/telemetry` | `telemetry-noop@stdout` | `telemetry-otlp@otlp` | `telemetry-otlp@collector` |
| `ggg/audit-export` | `audit-export-noop@noop` | `audit-export-otlp@otlp` | — |
| `ggg/llm` | `llm-fake@fake` | `llm-openai-compatible@openai` | — |

Defaults are the `ggg/profile/minimal` seeds; a created project writes explicit
values into `gogogadget.json`. Change one with:

```sh
ggg provider set --provider ggg/mail:production=ggg/system/mail-smtp@smtp
ggg provider list --json                     # committed selections, key names, never values
ggg provider configure --slot ggg/mail --environment development --set SMTP_HOST=localhost
ggg provider test --slot ggg/database --environment production
```

Selection is per environment, and all three environments' adapters compile
into one binary — only the branch matching `APP_ENV` initializes. A
development-only target selected for production is a planner refusal, not a
runtime surprise.

## Features

- **Auth & teams** — social OAuth, 2FA, orgs, roles and invitations through the
  `ggg/identity` slot; the synthetic development adapter needs no account, and
  internal ids (`usr_…`, `org_…`) are provider-neutral, mapped to provider
  subjects in `identity_subjects` / `identity_organizations`.
- **Billing** — checkout, portal, entitlements, dunning and trial emails,
  webhook state machine. `billing-local` runs the whole flow in-app for
  development and test; `billing-polar` is the managed reference.
- **App shell** — dashboard, projects CRUD (the canonical example), activity
  feed, settings (account/org/billing/API/webhooks).
- **i18n** — en + es with `?lang=`/cookie/`Accept-Language` detection, `i18n.T`
  in every template, catalogs generated from manifest `locales` blocks.
- **File storage, in-app notifications, outbound webhooks, usage metering,
  feature flags, search, realtime** — each a seam with a Postgres-or-local
  implementation and a managed alternative, selected per environment.
- **Frontend (htmx 4)** — boosted navigation scoped to one content box with
  View Transitions, morph swaps that keep table rows' DOM nodes, server-driven
  `HX-Location` navigation that never rebuilds the shell. No bundler, no
  hydration.
- **Admin** — users/orgs/MRR stats, audit-log viewer, jobs viewer with
  dead-letter requeue, flags and schedules admin, announcement banners,
  reason-gated impersonation.
- **Public API** — org-scoped Bearer tokens (`ggg_…`), `/api/v1`, cursor
  pagination, idempotency keys, per-token rate limits, OpenAPI 3.1 at
  `/api/v1/openapi.yaml` kept in step with the router by tests.
- **Platform** — Postgres job queue and schedules, audit log with configurable
  retention, strict CSP, CSRF, structured logs, `/healthz` + `/readyz` +
  `/metrics`.
- **Typed UI catalog** — 174 templ renderers in `internal/web/templates/ui`,
  one options struct each (`templ Badge(o BadgeOpts)`), closed enums, and
  `Attrs` with no arbitrary-attribute escape hatch. Live reference at
  `/dev/gallery`, twelve product scenarios at `/dev/scenarios`, both feeding
  the generated visual and axe matrices.

## Modules and the `ggg` CLI

The catalog publishes **297 modules** (22 elements, 125 components, 36 pages,
28 workflows, 86 systems). This repository installs 288 of them: 257 pulled by
`ggg/profile/full`, 30 by provider selection, and one deployment module.

```sh
ggg catalog --kind component     # every module and its state
ggg info ggg/component/data-table  # contract, files, links, verify commands
ggg add ggg/component/kanban     # install it and its dependency closure
ggg diff                         # what have I changed since install?
ggg update ggg/component/badge   # advance named modules only
ggg sync --check --offline       # does the tree match the lock?
```

Declared intent lives in `gogogadget.json`; resolved truth lives in the
committed `gogogadget.lock.json`, which records per file a `base_sha256` (what
upstream shipped) and a `local_sha256` (what is on disk) — that ledger is what
lets `update` tell your bytes from upstream's and refuse to clobber them.
Every command speaks `--json` with a fixed envelope and branchable exit codes
(`0` ok, `1` runtime, `2` usage, `3` refusal, `4` conflict, `5` rolled back),
so an agent can drive it without scraping output. `ggg` with no arguments on a
terminal opens the interactive console; `ggg help COMMAND` and the shell
completions are derived from the same command table the dispatcher reads.

Authoring is a command too:

```sh
ggg create resource invoice --scope org --api --admin --search
ggg create provider ledger --slot ggg/audit-export --package internal/ledger \
  --constructor NewModule --definition ledger.json
```

`ggg create` writes into the project's own mutable registry, then previews and
applies like any other mutation. Third parties publish their own signed
registries; nothing forks the core catalog.

- [CLI and registry](content/docs/cli.md) — commands, envelope keys, exit codes
- [Module anatomy and lifecycle](content/docs/modules.md) — manifests, closure resolution, the lock
- [Module removal and data retention](content/docs/module-removal.md) — policies and migration guarantees
- [Module reference](content/docs/module-reference.md) — every installed module, generated
- [UI foundations](content/docs/ui-foundations.md) — three layers, one options struct, closed enums
- [Component usage](content/docs/components.md) — finding a component, `native_fallback`, progressive enhancement
- [Component reference](content/docs/component-reference.md) — every signature, generated
- [Gallery and scenarios](content/docs/gallery.md) — `/dev/gallery`, `/dev/scenarios`, the generated test matrices

## Connecting managed services

Each managed target declares its own keys in its adapter's manifest, and the
generated [configuration reference](content/docs/configuration-reference.md)
and `.env.example` come from those declarations. Required keys are enforced
only for the adapter and target actually selected for the running environment;
a managed selection with missing keys is one joined boot error, never a silent
fallback to the local adapter.

| Slot | Managed reference | Keys |
|---|---|---|
| `ggg/identity` | Clerk | `CLERK_SECRET_KEY`, `CLERK_PUBLISHABLE_KEY`, `CLERK_WEBHOOK_SECRET`, `CLERK_PORTAL_URL` |
| `ggg/billing` | Polar.sh | `POLAR_ACCESS_TOKEN`, `POLAR_WEBHOOK_SECRET`, `POLAR_PRODUCT_PRO`, `POLAR_PRODUCT_TEAM` |
| `ggg/mail` | Resend | `RESEND_API_KEY`, `EMAIL_FROM` |
| `ggg/storage` | Cloudflare R2 | `STORAGE_R2_ACCOUNT_ID`, `STORAGE_R2_ACCESS_KEY_ID`, `STORAGE_R2_SECRET_ACCESS_KEY`, `STORAGE_R2_BUCKET` |
| `ggg/llm` | Any OpenAI-compatible API | `LLM_API_KEY`, `LLM_MODEL`, optional `LLM_BASE_URL` |
| `ggg/analytics` | PostHog | `POSTHOG_API_KEY`, `POSTHOG_HOST` |
| `ggg/observability` | Sentry | `SENTRY_DSN` |
| `ggg/database` | Neon | `DATABASE_URL` |

Development and test values the CLI manages live in gitignored,
mode-`0600` `.ggg/env/<environment>.env`; `ggg provider configure` writes them.
Production secrets are never written to disk by the tooling — they go to the
deployment target through `ggg deploy secrets`.

## Deployment

```sh
ggg deployment set ggg/system/deploy-fly
ggg deploy plan   --environment production
ggg deploy secrets --environment production --key DATABASE_URL --key CLERK_SECRET_KEY --yes
ggg deploy apply  --environment production
ggg deploy status --environment production
```

Two deployment modules ship: `ggg/system/deploy-docker` (the generated
`compose.yaml` / `compose.test.yaml` stacks; production is refused honestly)
and `ggg/system/deploy-fly` (`fly.toml`, flyctl with fixed argv). The image is
a multi-stage distroless build; migrations run on boot under goose's advisory
lock. See [/docs/deployment](content/docs/deployment.md).

## Documentation

The full docs ship **in the app** at `/docs` (and in `content/docs/`):
[getting started](content/docs/getting-started.md) ·
[architecture](content/docs/architecture.md) ·
[configuration](content/docs/configuration.md) ·
[authentication](content/docs/authentication.md) ·
[billing](content/docs/billing.md) ·
[testing](content/docs/testing.md) ·
[deployment](content/docs/deployment.md) ·
[security](content/docs/security.md) ·
[extending](content/docs/extending.md) (the authoring hub) ·
[roadmap](content/docs/roadmap.md) ·
[troubleshooting](content/docs/troubleshooting.md)

Three of those pages plus `.env.example` are generated from the module
manifests — `configuration-reference`, `module-reference`,
`component-reference` — so the inventory cannot drift from what is installed.
Do not edit them; edit the declaring module's manifest and run `ggg generate`.

## License

MIT — fork freely.
