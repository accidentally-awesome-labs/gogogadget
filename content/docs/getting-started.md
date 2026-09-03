---
title: Getting started
description: From ggg new to a running application in ten minutes — with zero SaaS accounts.
section: Start
weight: 2
---

Prerequisites: **Go 1.26+** and **Docker** (for the local Postgres). Nothing
else — no node, no npm.

A GoGoGadget project is created, not cloned. You choose a profile, choose one
adapter and service target for each required provider slot in each
environment, preview the plan, apply it, and own the source that lands.

## Create a project

```sh
git clone https://github.com/gogogadget/gogogadget && cd gogogadget
go build -o /tmp/ggg ./cmd/ggg
/tmp/ggg new ../my-app \
  --module example.com/my-app \
  --profile saas \
  --registry directory:. \
  --deployment ggg/system/deploy-docker
```

`--registry` takes `github:OWNER/REPO` or `directory:PATH`. A GitHub source is
pinned by `--ref` and verified against the core registry's Ed25519 public key;
a released `ggg` defaults the ref to its own version tag, and a development
build refuses to guess — pass `--ref` explicitly. `directory:.` is the
self-hosting form used above, which vendors the resolved catalog into the new
project so it needs no network at all.

Every answer has a flag, and `--answers FILE` supplies the same
`{Name, Module, Profile, Providers, Deployment, Registry, Ref}` object as JSON
(mutually exclusive with the individual answer flags, and incapable of
carrying a secret). On a terminal, missing answers are prompted for;
`--non-interactive` refuses instead of prompting, and `--json` implies
noninteractive.

To adopt a directory you already have, use `ggg init` instead: `--module` for
a directory with no `go.mod`, `--adopt` to produce the initial lock from what
is already installed, and `--claim PATH` for a pre-existing file that already
differs from what the module ships.

### The profiles

| Profile | Members | Required provider slots | What it adds |
|---|---|---|---|
| `minimal` | 34 | 18 | The smallest compilable application plus every provider seam it needs |
| `web` | 167 | 9 | Public content, internationalization, discovery surfaces |
| `api` | 170 | 10 | Identity and the JSON API transport |
| `saas` | 296 | 18 | Organizations, billing, jobs, notifications, admin, product workflows |
| `full` | 286 | 18 | Every product module in the catalog. It is not the largest list: `saas` names the nine environment-selected adapter modules explicitly, which `full` leaves to the provider selections, and `full` also drops the two modules this repository excludes |

A profile also carries **provider defaults** — the local adapter for
development and test, the managed one for production — and a
`default_deployment`. Those seed the wizard; the created project writes
explicit values, so no selection is ever implicit in your `gogogadget.json`.

## Run it

```sh
cd ../my-app
/tmp/ggg setup       # tools, modules, generation, and bin/ggg
bin/ggg services up
bin/ggg db migrate
bin/ggg db seed
bin/ggg dev
```

Open http://localhost:8080.

- **`ggg setup`** runs `go mod download all`, installs every tool artifact the
  installed manifests declare (each digest-verified before a byte is written,
  into project-relative `bin/`), completes the module graph with
  `go mod tidy`, generates, and builds `bin/ggg` from the project's own
  `cmd/ggg`. It is the only step that needs an external `ggg`; every later
  command rides `bin/ggg`.
- **`ggg services up`** starts the local services your selections actually
  need, from the generated `compose.yaml`. Nothing is hand-written: service
  names, images (digest-pinned), ports, volumes and health checks come from
  the `local_service` block of the selected target. `--environment test`
  selects `compose.test.yaml` instead.
- **`ggg db migrate`** runs the embedded goose migrations; **`ggg db seed`**
  loads the module-owned development fixtures through `cmd/seed`.
- **`ggg dev`** regenerates, brings the development services up healthy, then
  supervises templ watch, Tailwind watch and air as one process group. Each
  log line is prefixed by its process, Ctrl+C cancels the group, and the first
  non-cancellation child failure is what the command returns. Browser refresh
  stays manual — the strict CSP forbids air's injected reload snippet.

`make` targets are thin aliases: `make dev` is `bin/ggg dev`, `make check` is
`bin/ggg check`, `make seed` is `bin/ggg db seed`, `make db-reset` is
`bin/ggg db reset --yes`.

## Zero-account mode

A new project runs the **full application with zero SaaS accounts**, because
the development and test selections are local adapters: `identity-dev`,
`billing-local`, `mail-dev` (writes to `tmp/emails/`), `storage-filesystem`
(writes to `tmp/uploads/`), Postgres flags/search/realtime/notifications,
`observability-log`, `analytics-noop`, `llm-fake`, and memory cache and rate
limiting.

`.env.example` ships `DEV_AUTH_BYPASS=true`, which swaps the identity verifier
for one that accepts synthetic session cookies of the shape
`e2e:<userID>:<orgID>:<role>`.

Go to http://localhost:8080/dev/login — it sets the demo session cookie and
lands you in `/app` as the seeded demo user, admin of the demo org. (Hitting
`/login` or `/signup` in this mode redirects to `/dev/login` too.)

`DEV_AUTH_BYPASS` is honored only outside production: booting with
`APP_ENV=production` and the bypass on is a hard startup error. Every guard
and middleware still executes in bypass mode, so tests and e2e exercise the
real request path.

## Connect a managed service

Nothing degrades silently. Selecting a managed adapter for an environment and
leaving its keys unset fails the boot; it never falls back to the local
adapter. Keys the manifests mark `production_required` — today `DATABASE_URL`,
`NEON_API_KEY`, `RESEND_API_KEY`, the four `STORAGE_R2_*` and the four
`CLERK_*` — are collected by the generated validator and reported as one
joined error naming all of them. The Polar, PostHog, Sentry and
OpenAI-compatible adapters check their own keys inside their constructors, so
those fail on the first one reached.

```sh
bin/ggg provider list          # one row per slot and environment: adapter@target, mode, automation, key names
bin/ggg provider list --json   # the same rows as the machine envelope
bin/ggg provider set --provider ggg/mail:production=ggg/system/mail-resend@resend
bin/ggg provider configure --slot ggg/identity --environment production
bin/ggg provider test --slot ggg/database --environment production
```

`provider configure` renders the fields the selected target declares, refuses
a key that target never declared, validates each value against its declared
type (`string`, `url`, `integer`, `boolean`, `enum`), and reports configured
and missing **key names** — never values. Values it writes go to gitignored,
mode-`0600` `.ggg/env/<environment>.env`; only `development` and `test` are
CLI-managed, and a `--set` against production is refused outright. Production
values reach the platform through `ggg deploy secrets`, never a local file.
See [Deployment](/docs/deployment).

All keys and defaults: [Configuration](/docs/configuration) and the generated
[configuration reference](/docs/configuration-reference).

## The day-to-day loop

```sh
bin/ggg check   # generate → drift check → vet → test → build
```

`check` regenerates first (`ggg generate`: refresh mutable registries, sync,
templ, sqlc, Tailwind), then proves the tree matches the lock with
`ggg sync --check --offline`, then vets, tests and builds. Run it before every
commit. Other commands you will use daily:

```sh
bin/ggg test unit          # go test ./...
bin/ggg test e2e           # test compose stack + Playwright
bin/ggg db reset --yes     # destroy and recreate the local database, reseed
bin/ggg diff               # every file whose bytes differ from the lock
bin/ggg doctor --runtime   # lock, drift, provider keys, provider health, backups
```

`db reset` is the only ordinary command that deletes a database volume, which
is why it requires `--yes` in a noninteractive run. `services down` keeps
named volumes unless you pass `--volumes`.

Next: [Architecture](/docs/architecture) for how manifests, the resolver and
the generated bootstrap fit together, or [Extending](/docs/extending) for the
authoring path.
