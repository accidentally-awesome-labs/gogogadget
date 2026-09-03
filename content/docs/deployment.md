---
title: Deployment
description: Choosing a deployment module, the plan/confirm/apply contract, secrets discipline, and the Docker and Fly references.
section: Guides
weight: 24
---

The whole application ships as **one static binary**: templates, static
assets, migrations, blog, and these docs are embedded with `go:embed`. There
is no runtime dependency on a checkout — the binary plus a Postgres URL is the
deployable unit.

Where it goes is a project decision like any other: `gogogadget.json` names
exactly one **deployment module**, and that module contributes one deploy
target the `ggg deploy` commands drive.

```sh
ggg deployment set ggg/system/deploy-fly
```

`deployment set` journals the change like any other mutation: the intent file,
the graph and the lock move together. `ggg new` requires the choice up front
(`--deployment MODULE`, defaulting to the profile's `default_deployment`), the
resolver refuses a second selected deploy module, and removing the selected
one before replacing it is refused.

Two modules ship:

| Module | Deploy id | What it drives | Production |
|---|---|---|---|
| `ggg/system/deploy-docker` | `docker` | The generated `compose.yaml` / `compose.test.yaml` stacks through fixed `docker compose` argv | Refused — the generated compose files are the local stack |
| `ggg/system/deploy-fly` | `fly` | `fly.toml` plus flyctl with fixed argv | Yes |

## The command surface

```sh
ggg deploy plan     --environment production
ggg deploy apply    --environment production
ggg deploy status   --environment production
ggg deploy logs     --environment production [--follow]
ggg deploy rollback --environment production --yes
ggg deploy secrets  --environment production --key DATABASE_URL --key CLERK_SECRET_KEY --yes
```

`--environment` defaults to `production`. Every mutating form accepts
`--resume RUN_ID`; `--key` is repeatable and is the only way to name a secret.

- **`plan` and `status`** both take the target's authoritative reading —
  release id, URL, observed version, readiness — and report it under the
  `deployment` payload key. They write nothing. A target that is not ready
  exits 3 naming the state.
- **`apply`** computes the change set from the target (`Plan`), previews it,
  confirms, re-observes, and only then ships. Its confirmation is
  interactive: `deploy apply` asks on a terminal and refuses a noninteractive
  run. `rollback` and `secrets` take `--yes` for a noninteractive run and
  refuse without it.
- **`logs`** streams through the target; `--follow` keeps streaming until the
  command is cancelled.
- **`rollback`** refuses when no release has been recorded for that
  environment — there is nothing to go back to.

### Stale plans are refused

Before applying, the confirmed plan's `ObservedStateHash` is compared against
a **fresh** `Status` reading. If the target changed between the confirmation
and the apply, the command refuses with `remote_plan_stale` and asks you to
re-run the plan. Nothing is shipped on a stale observation.

### Partial failure resumes, it never re-plans

Each run is persisted in `.ggg/state.json` before and after each change with
its plan hash and per-change status. A failure exits 1, reports which changes
applied and which are pending, and names the run:

```
error  remote_change_pending  deploy://fly/release: …; resume with --resume 01J…
```

`--resume RUN_ID` loads that persisted plan and its hash and continues; it
does not compute a new plan, and it does not re-confirm. Every change carries
an idempotency key, so a resumed change that already landed is reconciled
rather than duplicated.

### The envelope

Remote changes ride the same fixed envelope every other command uses, with
`class: "remote"` and a path in the remote grammar
(`deploy://<deploy-id>/<resource-id>`, `provider://<adapter>@<target>/<resource-id>`).
Values are never included — only key names.

## Secrets discipline

Four rules, all enforced rather than advisory:

1. **Committed choices live in `gogogadget.json`.** Which adapter, which
   target, which deployment. No credential can be expressed there.
2. **CLI-managed development and test values live in `.ggg/env/<environment>.env`**,
   created at mode `0600` in a gitignored directory and written only by
   `ggg provider configure`. Resolution order is process environment → that
   file → the legacy `.env`, and the legacy file is read in development only.
   **No file is opened at all when the environment is production.**
3. **No secret ever enters a plan, an argv, an envelope or `.ggg/state.json`.**
   Plans list key *names*; the state file records resource ids, plan hashes
   and check timestamps. `ggg deploy secrets` reads the named values from the
   resolved environment and hands them to the target over stdin — `fly secrets
   import` for the Fly target — so a value never appears on a command line or
   in output. Every declared secret is passed through the redactor before
   anything is printed.
4. **The tooling never writes a production secret to disk.**
   `ggg provider configure --environment production --set …` is refused
   outright, and the Docker target refuses production secrets entirely,
   because the generated compose stack is not where production configuration
   belongs.

## Fly.io

`fly.toml` is owned by `ggg/system/deploy-fly`. The target reads the app name
from it and drives flyctl with fixed argv only.

```sh
fly launch --no-deploy          # once, to create the app
ggg deploy secrets --environment production \
  --key APP_URL --key DATABASE_URL --key CLERK_SECRET_KEY \
  --key CLERK_WEBHOOK_SECRET --key CLERK_PORTAL_URL --key CLERK_PUBLISHABLE_KEY --yes
ggg deploy apply --environment production
ggg deploy status --environment production
```

Every key a selected adapter marks required in production must be present, or
the boot refuses with one error naming all of them — a misconfigured deploy
fails at boot, not on the first request. Which keys those are depends on which
adapters you selected; `ggg provider list --json` reports them per slot and
environment, and the generated
[configuration reference](/docs/configuration-reference) describes each one.

`ggg deploy rollback` re-ships a recorded prior release through
`fly releases rollback`. Fly keeps releases immutable, so this is a redeploy
of recorded history rather than a mutation of it.

## Docker

`make docker-build` builds `gogogadget:local`. The image is multi-stage so the
final artifact carries no toolchain:

| Stage | Base | What happens |
|---|---|---|
| tools | `golang:1.26-bookworm` | Downloads the pinned Tailwind standalone binary for `TARGETARCH`, sha256-verified against a per-arch digest |
| build | `golang:1.26-bookworm` | `go mod download` → `go tool templ generate` → `go tool sqlc generate` → `tailwindcss --minify` → `CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=$VERSION" ./cmd/server` |
| final | `gcr.io/distroless/static-debian12:nonroot` | The binary only. `EXPOSE 8080`, `USER nonroot` |

- The build stage calls templ, sqlc and Tailwind **directly** rather than
  through `ggg generate`: the CLI resolves the Tailwind binary at a
  project-relative `bin/` path that does not exist in the image, and the
  registry-owned aggregates are committed and proved fresh by `ggg check`.
- The `-X main.version` stamp surfaces on `GET /healthz` as
  `{"status":"ok","version":"…"}` — deploy provenance for free.
- Distroless `nonroot`: no shell, no package manager, runs unprivileged.
  Everything served (templates, `static/`, `content/`, migrations) is embedded.

`compose.yaml` and `compose.test.yaml` are **generated**, not hand-written.
`ggg/system/docker` renders them from the local services the selected targets
declare: digest-pinned images, adapter-target-scoped service and volume names,
declared ports and health checks, and `env_file: .ggg/env/<environment>.env`.
A host-port or name collision refuses generation. Edit a target's
`local_service` declaration, never the YAML.

```sh
ggg services up --environment development    # compose.yaml
ggg services status
ggg services logs
ggg services down            # keeps named volumes
ggg services down --volumes  # removes them
```

## Database lifecycle

`db.Migrate` runs the embedded goose migrations on every boot, before the
server accepts traffic. It is idempotent, and goose's **advisory lock** means
N instances booting against one database migrate exactly once while the rest
wait. Roll forward by adding a migration and deploying; there is no separate
migrate step to forget.

Backup and restore go through the selected database target's operator:

```sh
ggg db backup       --environment development --destination backups/
ggg db restore      --backup <id> --to-env RESTORED_DATABASE_URL --yes
ggg db restore-drill --backup <id> --yes
```

`backup` records id, location, SHA-256 and timestamp in `.ggg/state.json`.
`restore` and `restore-drill` are container mutations and follow the same
plan/confirm contract as a deploy (`--yes` required noninteractively).
**Restore always creates a new database and verifies it; it never overwrites
the active one**, and the destination URL is delivered through an environment
key rather than argv or output. `ggg doctor --runtime` reports
`backup_missing` while no backup has ever been taken.

## /healthz vs /readyz

| Endpoint | Semantics | DB | Use for |
|---|---|---|---|
| `GET /healthz` | Liveness: the process is up; returns the build version | never touched | Probes that must not flap on a DB blip |
| `GET /readyz` | Readiness: 200 only when a database ping succeeds, else 503 | ping | Platform health checks |

A database hiccup must never restart the container (liveness), but it must
stop traffic (readiness). Point the platform's health check at `/readyz`.

Beyond the probe, `Runtime.Health(ctx)` aggregates every registered adapter
check — concurrently, with a 2-second deadline each and a 10-second cache —
and only a **critical** slot (`ggg/database`, `ggg/identity`, `ggg/billing`)
can make the report unready. `ggg doctor --runtime` is the operator-facing
view of the same territory: CLI/schema compatibility, drift, selected provider
keys, live provider checks, deployment linkage, backup policy and the
CLI-managed env files.

## Scaling

The app is **stateless** — cookie-carried session claims, a Postgres-backed
job queue — so horizontal scaling is a machine count. Two things to know:

- **The default rate limiter is per process.** Two machines double the
  effective per-IP limit. Scaling past one machine is a provider selection,
  not a code change: `ggg provider set --provider
  ggg/rate-limit:production=ggg/system/rate-limit-redis@upstash`.
- **The job worker is already multi-node safe.** Claims use
  `FOR UPDATE SKIP LOCKED` with a 5-minute visibility timeout, so every
  instance can run its embedded worker against one table without
  double-processing. See [Background jobs](/docs/background-jobs).
