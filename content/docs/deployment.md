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

- **`plan`** calls the deploy target's own `Plan` and reports the ordered
  change set under the `deploy_plan` payload key: the plan hash, the observed
  state hash the later apply is confirmed against, and one row per change in
  the `deploy://<deploy-id>/<resource-id>` path grammar. Secret keys appear by
  name; no value ever does. It writes nothing, and its run id is the one
  `--resume RUN_ID` reloads.
- **`status`** takes the target's authoritative reading — release id, URL,
  observed version, readiness — and reports it under the `deployment` payload
  key. It writes nothing. A target that is not ready exits 3 naming the state.
- **`apply`** computes the change set from the target (`Plan`), previews it,
  confirms, re-observes, and only then ships.
- **Confirmation is uniform across `apply`, `rollback` and `secrets`.** On a
  terminal the command asks. Off a terminal — or under `--json` — `--yes` is
  the confirmation and `--resume RUN_ID` replays a run whose plan was already
  confirmed; neither present is a refusal with exit 3, before anything runs.
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
ports and health checks derived from the declarations, and
`env_file: .ggg/env/<environment>.env`. Edit a target's `local_service`
declaration, never the YAML.

### Which host ports each stack publishes

Both stacks have to be able to run at once — a project that cannot run its own
test stack while its development stack is up has no working test story — so the
published ports are derived, not declared twice:

| Service | development | test |
|---|---|---|
| every selected local service | the target's declared `default_host` (`5432` for Docker Postgres) | that port **+ 10000** (`15432`) |
| `app` | `8080` | not published |

The offset is a round 10000 so the shifted port stays recognisable in
`docker ps` — `5432` → `15432`, `1025` → `11025`. Nothing reaches the test
stack's app over a host port (`ggg test e2e` runs the server on the host at
`:18080`, and CI's e2e job uses a service container and no compose at all), so
it publishes none; publishing it would take the development port and land on
`18080`, the exact port Playwright's `webServer` reuses instead of the server it
builds. `APP_URL` follows the effective port, and an unpublished app reports
its in-network origin (`http://app:8080`) because that is the only place it can
be reached from. `DATABASE_URL` inside a stack always addresses the database by
service name and container port, so it is unaffected by host publishing.

### Moving a published port

A busy host — anything already on `5432` or `8080`, including a Postgres
installed the normal way — moves a port with a committed project decision, not
by editing a generated file:

```json
"ports": {
  "ggg/system/database-postgres@docker-postgres/postgres": { "development": 5433 },
  "app/http": { "development": 8081, "test": 18081 }
}
```

A key is `<service>/<port>`: `app/http` for the generated app service, and
`<adapter>@<target>/<declared port name>` for a local service. Each key sets
`development`, `test`, or both; an unset environment keeps the derived port,
and an `app/http` test entry is the one way to publish the test app. The values
are host ports, they are not secret, and `APP_URL` follows them.

Two refusals hold the promise, both before anything is written:

- An override that names no port the environment's stack declares refuses,
  naming the ports it does declare. A silently dropped override leaves the
  service on the port you moved it off.
- **Two environments publishing the same host port refuse generation**, naming
  both environments, both owners and the port — as does a collision inside one
  file. Each file is its own Compose project, so the generated set is checked as
  a whole; a host has one port space.

A host port that a container already holds is still a runtime failure from
`docker compose`, not a generation error: the generator is deterministic and
offline and never probes the machine. Move it with an override.

```sh
ggg services up --environment development    # compose.yaml
ggg services up --environment test           # compose.test.yaml
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
| `GET /readyz` | Readiness: the database ping, then the runtime health report | ping | Platform health checks |

A database hiccup must never restart the container (liveness), but it must
stop traffic (readiness). Point the platform's health check at `/readyz`.

`/readyz` is the HTTP consumer of the generated `Runtime.Health(ctx)` report,
handed to the HTTP surface as the `runtime.health` capability. The database
ping runs first — a pool that cannot answer makes every other check
meaningless — and then the report decides:

| Report | Status | Body |
|---|---|---|
| Every check healthy | 200 | `{"status":"ok"}` |
| An unhealthy **non-critical** slot | 200 | `{"status":"degraded","degraded":["ggg/mail"]}` |
| An unhealthy **critical** slot (`ggg/database`, `ggg/identity`, `ggg/billing`) | 503 | `{"status":"critical slot unhealthy","critical":["ggg/database"]}` |

So a provider outage blocks readiness only for a slot whose seam declaration
is `critical: true`; everything else is reported degraded and keeps serving.
The body names slots, never check messages — a probe body is public. The whole
probe is bounded at 2 seconds and the aggregate report is cached for 10, so a
probe loop never fans out to every provider.

`Runtime.Health(ctx)` itself aggregates every registered adapter check
concurrently, with a 2-second deadline each, recovering a panicking check as
unhealthy. `ggg doctor --runtime` is the operator-facing view of the wider
territory: CLI/schema compatibility, drift, selected provider keys, live
provider checks, deployment linkage, backup policy and the CLI-managed env
files.

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
