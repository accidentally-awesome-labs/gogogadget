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

### What genesis leaves

`ggg new` is one journalled transaction, and it either leaves a project that
builds or it leaves nothing. Once the authored source, the lock and every
registry aggregate are written, genesis finishes the tree the way `ggg setup`
would: it installs the tool artifacts the installed manifests declare (each
digest-verified before a byte is written, into project-relative `bin/`),
completes the module graph with `go mod tidy`, and runs the generators the
lock declares — `templ generate`, `sqlc generate`, and the Tailwind build that
writes `static/app.css`.

Those outputs are inputs to compilation, not conveniences:
`internal/db/module.go` imports the package `sqlc generate` writes, and
`static/embed_registry_gen.go` names `static/app.css` in a compile-time
`//go:embed` pattern. A project without them cannot compile at all — including
the `bin/ggg` that `ggg setup` has to build before it can run anything else.

Genesis also writes `.ggg/env/development.env` and `.ggg/env/test.env` (mode
`0600`) with the declared development posture, which is what puts a created
project in zero-account mode. The values are declarations, not inventions:
every non-secret key whose module states an example differing from its
default — today that is `DEV_AUTH_BYPASS=true`. A secret is never written,
production is never written, and a file that already has content is never
touched again, because `ggg provider configure` owns it after creation.

Two files are easy to confuse. **`.env.example` is generated reference** and
nothing loads it. **`.ggg/env/<environment>.env` is what the stack actually
reads**: the generated `compose.yaml` names it as the app service's `env_file`,
and the CLI reads it after the process environment. It is gitignored.

So the last thing genesis does is `go build ./...` in the created project. If
that fails the genesis rolls back — a directory `ggg new` created is removed
outright — and the diagnostic names the check instead of reporting success
over a tree no command can run.

### The profiles

| Profile | Members | Closure | Required provider slots | What it is |
|---|---|---|---|---|
| `minimal` | 50 | 167 | 18 | The smallest closure that **boots** — not a small application. See the floor below |
| `web` | 190 | 254 | 18 | Minimal's floor plus public content, internationalization and the discovery surfaces |
| `saas` | 296 | 289 | 18 | Web plus organizations, billing, jobs, notifications, admin and the product workflows — the largest closure here |
| `full` | 286 | 288 | 18 | Every product module plus the registry-publishing template. The name overstates it; see below |

**Members** is what a profile names; **closure** is what installing it
actually resolves to. The two differ in both directions. Members that are
adapter candidates do not enter the closure unless the provider selections
choose them — which is why `saas` names 296 and installs 289 — and a seam
pulled in only through some member's `requires` enters without being named,
which is why `minimal` names 50 and installs 167. That second direction is
also why every profile requires all 18 slots: a seam pulled in transitively
declares its slot just as loudly as one named in the list.

The profiles are otherwise nested — `minimal ⊂ web ⊂ saas` — with exactly one
exception, and it is a provider default rather than a member: `minimal`
selects `mail-smtp` for production where `web` selects `mail-resend`, so
`minimal` installs one module `web` does not.

These numbers are asserted, not remembered.
`TestTheDocumentedProfileTableMatchesWhatTheProfilesResolveTo` in
`internal/modkit` plans every shipped profile and fails on the commit that
moves a count without moving this table.

#### Why `minimal` is 167 modules

Because the floor is not a taste decision, and `minimal` is where you see it.
What it no longer contains is a hand-curated component list: every page,
workflow and seam now declares the components and elements it actually
renders, derived with `go/types` and held by
`TestTheDeclaredUIEdgesAreTheOnesTheCodeReferences`, so a profile installs the
UI its own pages reach and nothing else. `minimal` named 67 UI modules and
installed 77 when that list was maintained by hand; it now names **none** and
installs **66**, and `ValidateUIComponentRequires` refuses at plan time when a
payload renders a component its module has no declared path to — the failure
that silence used to hide until the compiler found it.

What remains is not the UI graph. It is four seams that name product symbols
in their own payloads, and the pages those drag in:

- **`ggg/system/server`.** It owns `internal/web/server.go`, whose
  statically-declared capability fields name thirteen capabilities, so its
  `requires` pull `internal/{api,audit,cache,jobs,notify,ratelimit,`
  `realtime,search,telemetry,usage,webhooks}` into *every* closure that serves
  HTTP. `internal/web/routes.go` dereferences the `apiSurface` that
  `ggg/workflow/openapi-contract` declares, and `auth.go` and `htmx.go` call
  `applyStoredAppearance`, `applyImpersonation` and `resolveTheme`, which
  `ggg/workflow/appearance` and `ggg/workflow/impersonation` own — so a shell
  cannot be installed without a theme picker and an impersonation banner.
- **`ggg/system/jobs`.** `internal/jobs/definitions.go` names `deliverWebhook`,
  `exportProjectsCSV` and `exportOrgJSON`, owned by
  `ggg/workflow/{outbound-webhooks,project-export,organization-export}`.
- **`ggg/system/api`.** `internal/api/projects.go` compiles against
  `ggg/workflow/projects`' sqlc queries, so a JSON API implies a projects
  resource.
- **The schema assertions.** Migration `0020_provider_neutral_ids` asserts a
  fixed fifteen-table, twenty-three-column shape *before* it renames anything,
  so no closure may omit the nine modules that create those tables. It is
  immutable and in the lock, so the way down is a follow-on migration that
  neutralizes the assertion, not an edit.

Each of those pulls a page, and **navigation is a total order**: a
`runtime.navigation` entry may declare `after` another entry, generation
refuses an `after` naming an entry its area does not contain, and so each page
in a chain declares a hard `requires` on its predecessor. The settings sidebar
(`account → org → billing → api → webhooks`), the admin sidebar
(`overview → users → orgs → flags`), the app sidebar
(`dashboard → projects → files`) and two footer chains are entered whole.
That, and not the component catalog, is why `minimal` installs twenty pages.

A profile whose member list drops those pages plans cleanly and then fails
`go build` in the created project, which is what CI's `profiles` job exists to
catch: measured, a `minimal` that names five pages resolves to 131 modules and
49 UI modules and does not compile. Generating `server.go`'s capability struct,
deriving `definitions.go` from the installed job set, moving
`internal/api/projects.go` to the module whose queries it uses, and
neutralizing the migration assertion are what lower the floor for real. Until
then, `minimal` means *the least this source can be made to boot as*, and
saying otherwise would be advertising a shape that does not exist.

#### `ggg/profile/api` is gone, and `full` is not the whole catalog

**`api` was deleted.** It resolved to the same 254-module closure as `web`,
module for module, because the JSON API transport it existed to add is
something `ggg/system/server` requires. Two advertised names for one closure
is a lie no documentation fixes, so the name went rather than the explanation.
`ggg new --profile ggg/profile/api` refuses and names `web` as the
replacement; it does not fail as an unknown profile. Decoupling
`internal/web` from `internal/api` is what would make an API-only profile
mean something.

**`full` resolves 288 of the 297 modules the catalog publishes.** Six of the
nine absent are managed adapters its provider defaults do not select
(`mail-smtp`, `feature-flags-launchdarkly`, `notifications-knock`,
`search-typesense`, `usage-openmeter`, `webhooks-svix`), one is the deploy
module its `default_deployment` does not choose (`deploy-fly`), and two are UI
modules its member list omits (`ggg/component/table-empty`,
`ggg/element/divider`) — which is the only reason `saas` resolves one module
more. An adapter is a per-environment selection, so **no** profile can resolve
all 297; the name is the problem, not the member list, and renaming it is a
deliberate decision rather than a passing one.

A profile also carries **provider defaults** — the local adapter for
development and test, the managed one for production — and a
`default_deployment`. Those seed the wizard; the created project writes
explicit values, so no selection is ever implicit in your `gogogadget.json`.

## Run it

```sh
cd ../my-app
/tmp/ggg setup       # bin/ggg, plus every genesis step re-run idempotently
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
  `cmd/ggg`. Genesis already ran everything but that last build, and each step
  is idempotent — an already-verified tool install is left untouched — so on a
  fresh project this is effectively just the `bin/ggg` build. It is also the
  only step that needs an external `ggg`; every later command rides
  `bin/ggg`.
- **`ggg services up`** starts the local services your selections actually
  need, from the generated `compose.yaml`. Nothing is hand-written: service
  names, images (digest-pinned), ports, volumes and health checks come from
  the `local_service` block of the selected target. `--environment test`
  selects `compose.test.yaml` instead, which publishes each service on its
  declared port **+ 10000** (`5432` → `15432`) and does not publish the app at
  all — so both stacks run at once. If something on your host already holds a
  port, move it with a `ports` entry in `gogogadget.json` rather than editing
  the generated file; see [Deployment](/docs/deployment).
- **`ggg db migrate`** runs the embedded goose migrations; **`ggg db seed`**
  loads the module-owned development fixtures through `cmd/seed`. Both resolve
  `DATABASE_URL` in the documented order — process environment, then
  `.ggg/env/<environment>.env`, then `.env` in development only — and refuse
  when nothing supplies it rather than handing the tool an empty connection
  string, which libpq would quietly replace with its own defaults. See
  [Configuration](/docs/configuration).
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
bin/ggg check   # generate → stale-output refusal → drift check → vet → test → build
```

`check` regenerates first (`ggg generate`: refresh mutable registries, sync,
templ, sqlc, Tailwind) and then **refuses if generation moved anything** — a
generated file its declared source no longer produces is a failure, not a
silent repair, and the message names each path. Next it proves the tree
matches the lock with `ggg sync --check --offline`, then vets, tests and
builds. The test step reports `tests: N passed, M skipped, K failed across P
packages`: read the skip count, because `go test` prints `ok` for a package
whose every fixture skipped. Run `check` before every commit. Other commands
you will use daily:

```sh
bin/ggg test integration   # go test ./..., with the skip account
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
