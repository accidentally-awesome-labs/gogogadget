---
title: Extending GoGoGadget
description: The authoring hub — create, modify, update and publish modules with ggg, then the per-feature recipes.
section: Guides
weight: 27
---

Every change follows the same shape: find the **module that owns the file**,
edit that module's source, regenerate, and let `sync` prove nothing drifted.
New capability follows the same shape with one extra step at the front:
`ggg create` writes the module first.

That is the difference from a template you copy. The cross-cutting files — the
route table, the config struct, the i18n catalogs, the OpenAPI description,
`.env.example`, the compose stacks, the seed order, the Playwright surface
list — are not places you edit. They are rendered from the module manifests by
`ggg sync`, so a hand edit there survives exactly until the next
`ggg generate` and then vanishes without a word. Change the declaration
instead and the generated file follows.

```sh
ggg info ggg/workflow/projects   # who owns what, and what to run
# …edit the module's own source…
ggg generate                     # refresh mutable registries → sync → templ → sqlc → tailwind
ggg check                        # the gate: generate + no-drift + vet + test + build
```

This page is the task-shaped view. For the command surface in full see
[the ggg CLI](/docs/cli); for what a module is and how one is put together
see [Modules](/docs/modules); for what happens when one is uninstalled see
[Module removal](/docs/module-removal).

## Where the boundary is

| Kind | Examples | Rule |
|---|---|---|
| **Module source** | `internal/web/page_projects.go`, `internal/web/templates/ui/badge.templ`, `internal/db/queries/projects.sql`, `internal/billing/plans.go`, `input.css` | Yours. Edit freely; `ggg diff` reports it as `modified` and `update` never overwrites it. |
| **Declarations** | `registry/modules/<kind>/<name>/module.json` | The source of truth for routes, jobs, env keys, i18n, nav, data policy. |
| **Generated output** | `*_templ.go`, every `*_registry_gen.*`, `internal/db/sqlc/`, `static/app.css`, `static/ui-components.js`, `static/ui-engines.js`, `.env.example`, `compose.yaml`, `compose.test.yaml`, `content/docs/configuration-reference.md`, `content/docs/module-reference.md`, `content/docs/component-reference.md`, `e2e/generated/{inventory,personas,surfaces}.ts`, `internal/web/templates/scenarios_gen.go`, `internal/web/templates/ui/reference_gen.go` | Tool-owned. Never edit; `ggg sync --check` fails on drift. |

That list is not a convention — it is `IsGeneratedOutputPath` in
`internal/modkit`, and the planner refuses to let a module claim any path it
matches.

## Install what already exists

The catalog publishes 297 modules; this project installs 288 of them — 257
pulled by `ggg/profile/full`, 30 by provider selection, and one deployment
module. `ggg catalog` lists 297 rows, because the modules this project
excludes stay in the lock as `removed` tombstones so a later `ggg add` knows
what they were. Before writing anything, check whether what you need is
already there.

```sh
ggg catalog                          # every module and its state
ggg catalog --kind component         # one kind
ggg catalog --installed              # only what this project has
ggg info ggg/component/data-table    # files, deps, links, verify commands
```

`ggg info` answers the two questions you have next — where can I look at this,
and what do I run — as `gallery` / `scenario` / `route` links and literal
`verify` commands. Add `--json` to any command for the machine envelope
(`{ok, command, run_id, registry_commit, resolved, changes, generated,
conflicts, diagnostics, exit}`).

```sh
ggg add ggg/component/kanban         # installs it and its dependency closure
ggg remove ggg/component/carousel    # after the removal checks below
```

`add` and `remove` edit **only** `gogogadget.json` and then run the same
reconciler `sync` runs; they are not a second code path. This project selects
`ggg/profile/full` and subtracts, so `add` deletes an id from `exclude` and
`remove` appends one:

```json
{
  "schema": 2,
  "registries": [{ "namespace": "ggg", "source": "directory", "path": "registry" }],
  "modules": ["ggg/profile/full"],
  "exclude": ["ggg/component/table-empty", "ggg/element/divider", "ggg/system/deploy-docker"],
  "providers": {
    "ggg/mail": {
      "development": { "adapter": "ggg/system/mail-dev", "target": "filesystem" },
      "test":        { "adapter": "ggg/system/mail-dev", "target": "filesystem" },
      "production":  { "adapter": "ggg/system/mail-resend", "target": "resend" }
    }
  },
  "deployment": "ggg/system/deploy-fly"
}
```

Ids are globally scoped `<namespace>/<kind>/<name>`, so a third-party module
never shadows a core one. `providers` must name **exactly** the slots the
selected closure declares — no missing slot, no extra one — with a choice for
`development`, `test` and `production`. Adding a slot means installing the
seam that declares it; making one optional means removing that seam, not
leaving the selection blank.

`ports` is the other committed decision about how the stack stands up: the
host port one generated Compose port publishes on, keyed `<service>/<port>`,
for the environments that have a Compose file. Unlike the keys above it is
**optional** — absent means no override, which is what every project written
before it existed says — and the derivation rule and both refusals are in
[Deployment](/docs/deployment).

`gogogadget.lock.json` is the generated, committed counterpart: the registry
provenance ledger, the referenced snapshots, the deterministic dependency
`order`, one runtime order per environment, the resolved provider selections,
the managed Go dependency ledger, and per file a `base_sha256` (what upstream
shipped), a `local_sha256` (what is on disk) and a `state` — `clean`,
`modified`, `missing`, `conflicted` or `generated`. That pair is what makes
"did I change this?" a question with an answer.

Every command exits with a declared code, so automation can branch on it:
`0` ok, `1` fetch/runtime error, `2` usage or schema error, `3` a preflight
refusal, `4` safe modules updated but conflicts remain staged, `5` the
transaction failed and was rolled back.

## Modify installed source

Installed source is ordinary source. Open the file, change it, regenerate.
Nothing has to be told, and there is no fork:

```console
$ ggg diff
modified   ggg/page/home                internal/web/templates/home.templ
modified   ggg/system/static            static/app.css
```

`diff` lists every file whose bytes differ from the locked base, per module.
`ggg diff MODULE` narrows to one module; `--upstream` also lists clean files
and, when a conflict is staged, the path of its unified diff.

The one thing to keep straight: a local edit is invisible to the compiler but
loud to the updater. It never blocks you and it never gets clobbered — it
turns the next upstream change to that same file into a conflict you resolve
deliberately, which is the next section.

## Resolve an update conflict

```sh
ggg update                                   # every installed module, from its registry's declared ref
ggg update ggg/component/badge ggg/page/home # only these, plus the closure they require
ggg update --registry acme --ref v1.2.0      # move exactly one registry's ref
```

`ggg update` with module operands advances only those modules and whatever
they require, leaving everything else at its recorded per-module snapshot.
`--registry NAMESPACE --ref REF` is the other form: it moves exactly one
registry and takes no module operands. Either way, incompatibility is
reported by naming the modules that must move together.

Pristine files are replaced silently. A file you edited that upstream also
changed is **never** overwritten. Your bytes stay exactly as they are, the
complete upstream candidate and a unified diff are written under
`tmp/ggg/conflicts/<run>/<module>/`, the conflict is recorded in the lock, and
the command exits **4**. Independent modules that had no conflict still
advance; the conflicted module, its reverse dependents, and any dependency
whose `contract` also changed stay pinned at their old commit.

Read the diff, then pick one:

```sh
ggg resolve ggg/component/badge --path internal/web/templates/ui/badge.templ --accept-upstream
ggg resolve ggg/component/badge --path internal/web/templates/ui/badge.templ --keep-local
ggg resolve ggg/component/badge --path internal/web/templates/ui/badge.templ --merged
```

- `--accept-upstream` writes the candidate and records the file clean. Your
  edit is gone; you had the diff.
- `--merged` records the bytes currently on disk — merge by hand first — as
  `modified` against the **new** base.
- `--keep-local` keeps your bytes untouched but advances `base_sha256`,
  `revision`, `contract` and `source_commit` to the resolved upstream. The
  same conflict therefore clears for good, and the *next* upstream change to
  that file conflicts correctly instead of re-reporting this one.

Then `ggg sync` and the tree is green again. A conflict is
deliberately not portable: the candidate bytes live in ignored `tmp/`, so
`sync --check` keeps failing until someone resolves it and nobody can commit
a half-updated tree as a good state. If you clone a repo whose lock carries
conflict metadata but whose `tmp/` is empty, `ggg doctor` reports
`candidate_missing` and naming the modules; rerunning `ggg update` at the
lock's target commit re-materializes the candidates without touching your
source.

## Create source with `ggg create`

`ggg create` writes a complete, valid module into the project's own **mutable
directory registry**, then previews and applies it through the same planned
transaction every other mutation uses. A project created by `ggg new` gets
that registry automatically, with a namespace derived from the project slug;
core and third-party registries stay immutable.

```sh
ggg create resource invoice --scope org --api --admin --search
ggg create page pricing --scope public
ggg create job export-ledger --schedulable --max-attempts 5
ggg create component stat-tile --family data
ggg create migration backfill-invoices --owner acme/workflow/invoice --kind immutable
ggg create module system/ledger
ggg create provider ledger --slot ggg/audit-export \
  --package internal/ledger --constructor NewModule --definition ledger.json
```

| Form | What it emits |
|---|---|
| `resource NAME --scope user\|org\|platform` | The full vertical slice — see below |
| `page NAME --scope public\|app\|admin\|dev` | A templ page in `internal/web/templates/pages/` and its package claim |
| `component NAME --family FAMILY` | A `package ui` renderer in `internal/web/templates/ui/` |
| `job KIND [--schedulable] [--max-attempts N]` | A handler in `internal/jobs/` plus the `runtime.jobs` declaration and job-kind claim (attempts default to 10) |
| `migration NAME --owner MODULE --kind immutable\|neutralize\|purge` | A reviewed forward migration payload recorded in the owning module's ledger |
| `module KIND/NAME` | A bare module of that kind with one package and its claim |
| `provider NAME --slot SLOT --package PKG --constructor SYM --definition FILE` | An adapter manifest: slot, capabilities derived from the slot, targets, inputs, env, dependencies, lifecycle and health from the definition file |

`--definition` is required for `create provider` off a terminal, and the
resulting adapter must validate as a manifest before anything is written.

### What `create resource` actually emits

One invocation writes the sqlc query file, an immutable migration, the
transport, the templates, a validator test, and every declaration that binds
them together: routes, queries, navigation, locales, the visual surface, the
OpenAPI slice (with `--api`), the data-lifecycle record, the test inventory,
and the namespace claims. Names are derived once — table defaults to
`snake(NAME)+"s"`, route to `/app/<kebab-name>s`, both overridable with
`--table` and `--route` — so a route id, a query name and an i18n key cannot
disagree.

The flags narrow it, and three combinations are refused before a single file
is built:

| Refusal | Why |
|---|---|
| `--no-ui` with `--admin` | An admin surface *is* a UI surface; accepting both would either emit templates `--no-ui` promised to omit or declare admin routes with no renderer |
| `--api` with `--scope user` | `RequireAPIToken` puts an organization in the context and nothing else, so a user-scoped table has no tenant on the JSON transport — the read would list every user's rows to any token holder |
| `--search` with `--scope platform` | `search_documents.tenant_id` is a foreign key to `orgs`; a platform row belongs to no organization, and filing it under whichever staff member wrote it would both misattribute it and hide it |

Two narrowings are applied rather than refused, and both are visible on the
plan: a **platform**-scoped resource implies `--admin`, because its mutations
belong at admin scope rather than to every authenticated member of every
organization; and `--api` on a platform resource emits the **read** route
only, reported as the `resource_api_read_only` diagnostic naming
`/admin/<plural>` as where the mutations live.

After `create`, the normal loop applies: edit the emitted source, run
`ggg generate`, run `ggg check`.

## Author a module

A module is one directory: `registry/modules/<kind>/<name>/module.json`, next
to the source files it claims. There is no code in a manifest — no hook, no
postinstall, no command array. It is data the generators read.

```jsonc
{
  "schema": 2,
  "module": {
    "id": "ggg/workflow/widgets", "kind": "workflow", "name": "widgets",
    "revision": 1, "contract": 1,
    "title": "Widget create, update and delete",
    "description": "…",
    "requires": [
      { "id": "ggg/system/database", "contract": { "min": 1, "max": 1 } },
      { "id": "ggg/system/server",   "contract": { "min": 1, "max": 1 } }
    ],
    "dependencies": { "go": [], "tools": [], "containers": [] },
    "files": [
      { "source": "internal/web/workflow_widgets.go",
        "target": "internal/web/workflow_widgets.go",
        "class": "go", "sha256": "…", "rewrite_module": true, "contract": true }
    ],
    "claims":  { "routes": ["widgets.create"] },
    "runtime": { "routes": [ /* …  */ ] },
    "migrations": [], "environment": [], "docs": [], "tests": {},
    "data": [], "removal_policy": "free"
  }
}
```

The fields that carry weight:

- **`files`** — exclusive ownership. Two modules may never claim one path, and
  identical bytes never imply shared ownership. `contract: true` marks a file
  that defines the module's public interface, so changing it bumps the
  contract and pins dependents through an update. `rewrite_module: true` lets
  the installer rewrite a canonical Go import prefix — any configured
  registry's — into the derivative project's own module path.
  `self_host: true` (test payloads only) marks an assertion about the
  publishing repository itself, installed only where the project's module path
  *is* that registry's `canonical_module` — see
  [Modules → Files](/docs/modules#files).
- **`requires`** — a hard dependency edge with an inclusive contract range,
  `{id, contract: {min, max}}`. It is what makes `add` install a closure and
  `remove` refuse while a dependent is present, and an out-of-range contract
  refuses before a payload byte is read. `revision` moves on any
  implementation change; `contract` moves only when a consumer must change
  code.
- **`dependencies`** — `go` (exact `{module, version}`), `tools` (per-os/arch
  artifact with URL, SHA-256, format and project-relative `install_path` under
  `bin/`) and `containers` (image with an immutable digest). The lists are
  always present, even empty. Before the lock or `go.mod` moves, the planner
  scans authored and generated imports and refuses an undeclared direct
  dependency.
- **`runtime`** — the typed contributions: `routes`, `jobs`, `janitors`,
  `navigation`, `slots`, `ui`, `scenarios`, `queries`, `content_types`,
  `assets`, `visual`, `personas`, `system`, `provider_slots`, `provisioners`,
  `database_ops`, `deploy`, `cli`. Each generates real Go, so a wrong package
  or handler name is a compile error on a named generated line, not a mystery
  at boot.
- **`runtime.system`** — the constructor a system module contributes:
  `package`, `constructor`, its `needs` and `provides` capabilities, and
  `start` / `stop` / `health` flags. Add an `adapter` block (`slot` plus
  `targets`) and it becomes a selectable implementation of a provider slot;
  add `provider_slots` instead and it becomes the constructor-free seam that
  declares one.
- **`environment`** — one record per env key (`key`, `field`, `type`,
  `description`, plus `secret`, `default`, `required`, `production_required`
  and `targets`). This is the only place an env key is declared: the config
  struct, its validation, `.env.example` and the
  [configuration reference](/docs/configuration-reference) all come from here.
  `targets` narrows a key to specific service targets of the owning adapter —
  the parser is generated for the installed union, but `required` is enforced
  only for the adapter and target actually selected.
- **`locales`** — `{"en": {...}, "es": {...}}`, inline in the manifest. Every
  key must exist in every declared locale with matching format placeholders,
  and two modules may not own the same key. Generation refuses otherwise.
- **`data`** — one record per table (`scope`, `export`, `account_delete`,
  `organization_delete`, `persisted_jobs`, …). The account and organization
  export collectors and the deletion order are generated from these, so a new
  stateful module cannot be silently left out of a GDPR export.
- **`claims`** — the collision-checked names this module owns: packages,
  routes, jobs, and for the framework surfaces `provider_slots`,
  `provisioners`, `database_ops`, `cli` and `deploy`. Every declaration needs
  a matching claim.
- **`removal_policy`** — `free`, `retain-data`, `drain-required`,
  `replacement-required` or `major-version-only`. See
  [module removal](/docs/module-removal).

Then build and validate:

```sh
ggg registry build       # rescan the registry tree, refresh payload digests, verify vendored bytes
ggg registry validate    # check the catalog, then prove the closure lifecycle in a derivative
ggg sync --offline       # install into this tree and regenerate
```

`registry build` is the authoring step you will forget once: in a self-hosting
registry the payload and its manifest live in the same tree, so editing a
module's own source stales the manifest digest and every later `sync` refuses
with a `sha256 mismatch`. `build` re-reads each payload, rewrites the recorded
digest, and rebuilds the per-kind indexes by **scanning** `registry/modules/`
— which is what makes a newly authored module visible at all. It also verifies
every vendored artifact against its declared byte count and SHA-256, and
rejects `eval(`, `new Function(`, string-argument `setTimeout`/`setInterval`,
and references to origins the manifest did not declare.

## Publish your own registry

Authoring a module in this tree makes it yours. Publishing one makes it
everyone's, and it does not require forking the core catalog: a project can
configure any number of registries, each with its own namespace and its own
canonical Go module, and ids are globally scoped (`acme/system/…`) so nothing
shadows anything.

The maintained template is
[`templates/external-registry/`](https://github.com/gogogadget/gogogadget/tree/main/templates/external-registry).
Copy that directory to the root of a new repository and you have a complete,
signed, publishable registry: the `registry.json` root and one index per kind,
a seam adapter for the `ggg/audit-export` slot with two service targets, owned
environment keys, a declared dependency set, lifecycle and health hooks, its
contract tests shipped as `test`-class payloads, a `runtime.cli` contribution,
a signed `registry.snapshot.json`, and a CI workflow that runs the publisher
gate. Its `README.md` is the long form of what follows.

If you copied the template you already have a `registry.json` — edit its
`namespace` and `canonical_module` and skip `registry init`, which exists for
starting from an empty directory and never overwrites an existing file.

```sh
ggg registry init --namespace acme --canonical-module example.com/acme/ggg-registry
ggg registry keygen --private registry-private-key.b64 --public registry-public-key.b64
ggg registry build  --dir .          # refresh digests, rebuild indexes, write the snapshot
ggg registry sign   --dir . --key-file registry-private-key.b64
ggg registry verify --dir . --public-key "$(cat registry-public-key.b64)"
git tag -a v1.0.0 -m "acme registry v1.0.0"
```

`ggg registry validate` proves the full lifecycle — install, generate,
compile, run the module's declared tests, remove, compare the tree byte for
byte — but only for the registries the project it runs in has closures for.
This repository runs it against `templates/external-registry` on every change.
From your own repository, prove the same thing by installing into a scratch
consumer: `ggg new`, `ggg registry add directory:…` with the registry copied
in, `ggg provider set`, then `go build ./...` and the module's own
`go test`. The template's CI workflow does exactly that.

`sign` accepts **exactly one** of `--key-file` or the base64
`GGG_REGISTRY_SIGNING_KEY` environment variable, and refuses when both or
neither are set; CI uses the environment form so the private key never touches
a disk. Publish the **public** key — it is the string consumers pin.

`--dir` is explicit above because a publisher's registry is usually its whole
repository root. Omitted, it resolves to the directory that actually holds the
selected registry's `registry.json` — the declared registry path when that
directory is a self-contained registry root, otherwise the project root, which
is the rule `ggg sync` resolves the same registry through.

A consumer adds the source, then selects the adapter per environment:

```sh
ggg registry add github:acme/ggg-registry --namespace acme --ref v1.0.0 \
  --public-key "$(cat registry-public-key.b64)"
ggg provider set --provider ggg/audit-export:production=acme/system/audit-export-ledger@ledger-cloud
```

Remote registries must be signed: an unsigned tree is consumable only as an
explicitly configured project-relative `directory` source. `registry add`
previews the namespace, key fingerprint, canonical module, modules and their
dependencies before writing anything, and a tampered payload, a bad signature,
a namespace that does not match what you pinned, a colliding canonical module
prefix, and a dependency outside its declared contract range each refuse
before the first byte is written.

A tag is the right pin for both core and third-party registries, and it stays
a tag: `gogogadget.json` keeps `"ref": "v1.0.0"` because that is what a human
maintains and what `ggg update --registry NAMESPACE --ref REF` moves. The
commit it resolved to is recorded in the lock alongside the snapshot digest and
the content-addressed cache key, and offline commands — `ggg setup`,
`generate`, `check`, every `sync --offline` — resolve through that record
rather than the ref, because nothing offline can turn a tag into a commit. A
registry whose ref is a full commit must still resolve to the commit the lock
records; a disagreement refuses and names `ggg update`, so a pinned project
never moves as a side effect of `sync`.

Rotate a signing key with `ggg registry rotate --old-key-file … --new-key-file
… --not-before RFC3339`. That publishes `registry-key-rotation.json` plus
detached signatures under both keys; a consumer honours the new key only once
**both** verify and their clock reaches `not_before`, so a key can never be
swapped out from under a pinned one.

`revision` moves on any implementation change; `contract` moves only when a
consumer must change code. What every module declares — source namespace,
contract range, provider slot, targets, automation level, dependency set,
lifecycle, health and verification commands — is listed in the generated
[module reference](/docs/module-reference).

### How this repository publishes itself

The core catalog is a registry like any other, and `ggg new --registry
github:OWNER/REPO --ref TAG` fetches this tree and verifies it against the
public key compiled into the CLI (`coreRegistryPublicKey`). So
`registry.snapshot.sig` is a **published artifact here too** — it is committed,
not ignored. It was ignored once, on the theory that this catalog is only ever
consumed as a directory source, and every genesis from GitHub refused with
`read registry.snapshot.sig: no such file or directory`.

A release therefore runs, in this order, on the commit being tagged:

```sh
ggg registry build              # refresh digests and indexes, rewrite the snapshot
ggg registry sign   --dir . --key-file <core signing key>
ggg sync --offline              # settle the lock over the signed catalog
ggg registry verify --dir . --public-key "$(cat <core public key>)"
git tag -a vX.Y.Z -m "gogogadget vX.Y.Z"
```

`build` before `sign`, always: building rewrites the snapshot and invalidates
any earlier signature. `sync` after `sign`, always: the lock records the
catalog it installed, signature included, so signing after the sync leaves one
pending change and `sync --check` refuses.

`TestCommittedSnapshotVerifiesUnderThePinnedCoreKey`
fails when the committed signature is missing, stale relative to
`registry.snapshot.json`, or produced by a key the shipped CLI does not pin, so
a catalog change that forgets the re-sign cannot reach a tag.

The private half never enters the tree. Changing `coreRegistryPublicKey` is a
key rotation once anyone has consumed a tag: publish
`registry-key-rotation.json` through `ggg registry rotate` instead of editing
the constant.

## Rules that prevent data loss

Five rules, each with the reason it exists. They are enforced, not advisory.

**`.env` is never read or written by the tooling.** Environment keys are
declared in manifests, and `.env.example` is generated from them (its first
line says `Generated by ggg sync; DO NOT EDIT.`). A tool that edited your
`.env` would be a tool that can leak or destroy a production secret during a
routine `sync`, so it does not have the capability at all. Adding a key means
adding an `environment` record and copying the new line from `.env.example`
into your own `.env` by hand.

**Applied migrations are retained and forward-only.** `0001`–`0019` are
adopted with their exact filenames and content digests and declared
`kind: "immutable"`. A new logical migration is allocated the next free global
number **once** and keeps it forever; `update` never renumbers or rewrites
one. Goose records applied migrations by filename, so re-allocating a number
would silently re-run schema changes already applied in every deployed
database. Removing a module deletes its Go and SQL sources but leaves its
migration files, schema and rows in place, and `ggg` never connects to a
database or executes SQL — not even Goose Down.

**`update` never overwrites locally modified source.** See the conflict
section above. The reason is blunt: an installer that can silently revert a
customization is an installer nobody can safely run on a real product, and
the safe alternative is not "ask" — a prompt in CI is a hang — it is to
refuse, stage, and exit 4.

**Some modules cannot be removed by the CLI at all**, because removing them is
a migration rather than an uninstall:

| Policy | Modules (of the 288 installed here) | Behaviour |
|---|---|---|
| `replacement-required` (17) | `ggg/element/ui-core`, `ggg/system/{apphost,audit-export,cache,config,database,i18n,modkit,organizations,realtime,search,security,server,static,telemetry}`, `ggg/workflow/{appearance,auth-session}` | Refused (exit 3). There is nothing left to run without them; a provider seam has no nil form, so replacing one is a manual migration. |
| `major-version-only` (5) | `ggg/system/{api,billing,identity,rate-limit}`, `ggg/workflow/billing-webhook` | Refused (exit 3). `/api/v1` is a published contract and identity is every existing session; dropping either breaks live clients, so it belongs in a major version. |
| `drain-required` (1) | `ggg/system/jobs` | Allowed only if the manifest supplies a reviewed forward **neutralization** migration that disables schedules and terminally marks persisted work before the new binary starts. `--purge-data` additionally requires a reviewed teardown migration. |
| `retain-data` (16) | audit, content, notifications, schedules, storage (seam and S3 adapter), usage, webhooks, flags, announcements, impersonation, API tokens, notification preferences, outbound webhooks, blog, changelog | Removed, but their tables and rows stay. |
| `free` (249) | everything else | Removed cleanly. |

Removal also refuses (exit 3, before touching anything) when a module is
required by an installed dependent, when one of its owned files is missing,
or when one is locally modified — the diagnostic names `ggg diff MODULE`
and asks you to back the customization up or revert it deliberately. There is
no force flag.

**Advanced widget assets are optional, self-hosted and checksummed.** Three
modules vendor a browser engine, each recorded in its manifest with source
URL, version, exact byte count, SHA-256 and licence:

| Module | Artifact | Version | Bytes | Licence |
|---|---|---|---|---|
| `ggg/component/chart` | `static/vendor/chartjs-4.5.1.umd.min.js` | 4.5.1 | 208522 | MIT |
| `ggg/component/calendar` | `static/vendor/cally-0.9.2.js` | 0.9.2 | 38355 | MIT |
| `ggg/component/kanban` | `static/vendor/sortablejs-1.15.7.min.js` | 1.15.7 | 45478 | MIT |

Nothing loads from a CDN — the CSP is `script-src 'self'` and a browser test
asserts no cross-origin request. Each engine loads lazily, only when its
`data-ui-engine` root first appears, and every one of these components renders
a working accessible fallback without it: a chart also renders its data as a
semantic table, the date pickers wrap live native `date`/`datetime-local`
inputs, and a kanban card is movable through a keyboard "Move to…" menu that
posts the same form the drag does. Removing one of these modules deletes its
first-party source and its vendored bytes and regenerates the static
registries; the core component set pulls in no browser engine at all.

## Add a CRUD resource

`ggg create resource NAME --scope org` writes all of this for you — the
section above lists exactly what it emits and what it refuses. Read this one
when you are hand-building a resource, extending a generated one, or want to
know why the generator made a particular choice.

`ggg/workflow/projects` plus `ggg/page/projects`, `ggg/page/project-new` and
`ggg/page/project-edit` are the worked example; read them with `ggg info`
before copying. A hand-built resource is normally **two modules**: a page
module owning the read surface and a workflow module owning the mutations.

1. **Migration** — `internal/db/migrations/00NN_widgets.sql` with
   `-- +goose Up` / `-- +goose Down`. Org-scope it
   (`org_id TEXT NOT NULL REFERENCES orgs(org_id) ON DELETE CASCADE`)
   plus `created_at` / `updated_at`. Declare it in the manifest's
   `migrations`; the number is allocated once and pinned in the lock. See
   [Database](/docs/database).
2. **Queries** — `internal/db/queries/widgets.sql`, one file per table. Every
   UPDATE sets `updated_at = now()`; every WHERE carries `org_id = $1`
   so a cross-org id is a 404, never a leak.
3. **Templates** — `internal/web/templates/widgets.templ`, composed from
   `ui.DataTable`, `ui.PageHeader`, `ui.Field` and friends rather than raw
   markup. Stable row ids (`widget-42`) so morph swaps patch in place, and a
   `data-testid` on every element a test asserts on. See
   [Components](/docs/components).
4. **Handlers** — `internal/web/page_widgets.go` for reads and
   `internal/web/workflow_widgets.go` for mutations. Validation failure → 422
   plus the re-rendered form fragment; success → `Navigate` (soft, in-app) and
   `Toast`; row delete → 200 empty against `hx-target="closest tr"`. Reach for
   `Redirect`/`HXRedirect` only for another origin or a rebuilt document.
5. **Audit** — `audit.Log(ctx, s.q, orgID, userID, "widget.created", …)` on
   every mutation; it shows up on `/app/activity` for free.
6. **Routes and nav are declarations, not registrations.** Add a
   `runtime.routes` entry (`id`, `method`, `pattern`, `scope`, `policy`,
   `package`, `handler`) and claim the id under `claims.routes`; add a
   `runtime.navigation` entry with a `route_id` and a `label_key` for the
   sidebar. The app chain (`requireAuth → requireNotDisabled → requireOrg →
   loadPlan`) follows from `"scope": "app"`. Editing `internal/web/routes.go`
   does nothing: the mux is populated from
   `internal/web/routes_registry_gen.go`.
7. **Strings** — put every UI key in the manifest's `locales` block, in both
   `en` and `es`.
8. **Data policy** — one `data` record per new table, so exports and deletion
   include it.
9. **Tests** — `internal/web/widgets_test.go`: cross-org access → 404;
   plan-limited branch → 422. Name the package in the manifest's `tests` so
   `ggg info` prints the command. See [Testing](/docs/testing).
10. `ggg generate && ggg check`.

## Add a plan

Plan truth is one immutable catalog, and the provider product ids are bound to
it by the **adapter**, not by the seam. Both halves matter: the seam knows what
a Business plan *is*, and each billing adapter knows what a Business plan is
*called* at its provider.

1. **Plan truth** — append an entry to `defaultPlans` in
   `internal/billing/plans.go` (owned by `ggg/system/billing`). Order is render
   order; keep `free` first — `planCatalog.ByKey` falls back to the `free`
   entry for an unknown key, and `FreePlan()` reads `defaultPlans[0]`. Leave
   `ProviderProductID` empty here: it is provider-specific.
2. **Provider product** — create the product in the Polar dashboard; copy the
   id.
3. **Env** — add an `environment` record to **`ggg/system/billing-polar`**'s
   manifest (`"key": "POLAR_PRODUCT_BUSINESS", "field": "PolarProductBusiness",
   "type": "string"`). Every Polar key belongs to the adapter, never to the
   seam. Do **not** touch `.env.example` or the config struct; both are
   generated from that record. Copy the new line into your own `.env`, or use
   `ggg provider configure`.
4. **Adapter wiring** — extend the `switch p.Key` in
   `internal/billing/polar/module.go` with a `case "business"` that sets
   `p.ProviderProductID = d.Config.PolarProductBusiness`, then hand the plans
   to `billing.NewPlanCatalog`. Nothing else constructs a catalog for Polar.
5. **The local adapter needs nothing.** `billinglocal.LocalPlanCatalog` uses
   each plan's own key as its product id, so a new paid plan is checkout-able
   in development and test the moment step 1 lands.

The pricing page, upgrade CTAs and usage meters render from the injected
`billing.PlanCatalog`, and the webhook's product-id → plan-key reverse map is
built from `catalog.All()`, so both follow automatically. Enforcement
(`MaxProjects`, `MaxStorageMB`, the meters) applies the moment the plan exists.
See [Billing](/docs/billing).

`NewPlanCatalog` refuses an empty catalog, an empty plan key, a duplicate key
and a duplicate non-empty product id — so a copy-paste mistake in step 1 or 4
is a boot error naming the collision, not a plan that silently shadows another.

## Add annual pricing

1. Add an `Interval` field (`"month"` / `"year"`) to `billing.Plan` and set it
   on each `defaultPlans` entry.
2. A second provider product per paid plan, each with its own `environment`
   record on the adapter's manifest, then a matching `case` in the adapter's
   product-id switch.
3. Teach the webhook's product-id → plan-key reverse map (built from
   `catalog.All()` in `internal/web/workflow_billing_webhook.go`) that the
   annual ids map to the same keys, and persist the interval on the
   subscription row (new column, new migration).
4. Fix the math: `MRRWithCatalog` in `internal/billing/plans.go` must divide an
   annual subscription's price by 12.
5. Pricing page: group `ui.PlanCard`s by plan, toggle by interval.

## Add an email kind

1. **Templates** — the HTML and plain-text components in
   `internal/web/templates/emails.templ` (they share `EmailLayout`).
2. **Builder** — an `XMessage(locale, appURL, to, …) (mail.Message, error)`
   constructor in `internal/mail/mail.go`, next to `WelcomeMessage`. Bodies
   render to strings at enqueue time; workers never touch templates.
3. **Job kind** — a `Kind…` const plus a `jobs.Define` registration, and a
   `runtime.jobs` record on `ggg/system/mail`'s manifest:
   `{"kind": "email.x", "package": "internal/jobs", "handler": "defineEmailX",
   "schedulable": false, "max_attempts": 0}`. The generated dispatcher and
   typed enqueue helper come from that record — there is no `dispatch` switch
   to edit any more.
4. **Enqueue** at the trigger site with `jobs.EnqueueEmail(ctx, q, jobs.KindX,
   msg, orgID, runAt)`. Billing-triggered? Extend the `billing.EmailSink`
   interface and its implementation in `internal/web/email_sink.go` — billing
   must not import mail or jobs directly (import cycle).
5. **Verify locally** — DevSender writes `tmp/emails/*.html`; no Resend
   account needed. See [Email](/docs/email).

## Add a job kind

1. `jobs.Define[P](kind, schedulable, maxAttempts, handler)` in the owning
   module's package, with a typed payload struct. `maxAttempts == 0`
   normalizes to 8.
2. Declare it in that module's `runtime.jobs`. The dispatcher, the typed
   enqueue helper, the schedulable catalog and the admin choices are all
   generated from the declaration, so an undeclared kind simply is not
   dispatchable and an unknown persisted kind dead-letters immediately with
   `module_uninstalled` instead of burning eight retries.
3. Enqueue with `jobs.Enqueue` / `EnqueueAt` from the call site.
4. Test claim → complete and poison → attempts increment. Backoff
   (2^attempts minutes), the 5-minute visibility timeout and dead-lettering at
   `max_attempts` come free. See [Background jobs](/docs/background-jobs).

## Add search to a resource

1. Generated column and GIN index in a migration:
   `search_tsv tsvector GENERATED ALWAYS AS (to_tsvector('simple', <cols>)) STORED`.
2. Rewrite the list query with the FTS + ILIKE-fallback predicate and the
   `ts_rank` ordering — copy `ListProjectsByOrg` in
   `internal/db/queries/projects.sql` (see [Database](/docs/database)).
3. `ggg generate`, then a db round-trip test: exact word, websearch
   multi-word, partial token, ranking.

## Schedule recurring work

1. Add a job kind (above) with `"schedulable": true`. Scheduled payloads
   arrive wrapped in `jobs.SchedulePayload` — unwrap `.Payload`.
2. Insert a row via `schedules.Create`, or ship one as module-owned seed SQL
   under `internal/db/testdata/seed/`. `clerk_org_id NULL` is system-wide;
   `every_seconds >= 60`.
3. The worker's scheduler pass claims due rows every poll cycle — no daemon to
   configure. See [Background jobs](/docs/background-jobs) for missed-tick
   semantics.

## Add a webhook event

Unknown events are already ACKed (200 + log), so this is purely additive.

- **Clerk** — a parser for the payload shape in `internal/identity/sync.go`,
  then a `case` in `processClerkEvent`
  (`internal/web/handlers_webhooks.go`, owned by
  `ggg/workflow/identity-webhook-sync`). Test with the `signSvix` fixture, which
  emits real `svix-*` headers.
- **Polar** — a `case` in `Processor.ProcessSubscription`
  (`internal/billing/webhook.go`), reached from
  `internal/web/workflow_billing_webhook.go`. Test with `signStandard`, which
  emits real `webhook-*` headers.

Idempotency (`webhook_events`) and the 400/500 retry semantics apply
automatically. Signature verification is not optional; the two header families
are explained in [Security](/docs/security).

## Add an OAuth provider

A hosted identity provider's own dashboard → SSO connections → enable
Google/GitHub/…. **Zero code**: the hosted portal renders the buttons and the
mirror sync does not care how a user authenticated. 2FA is the same story.
See [Authentication](/docs/authentication).

## Swap a provider

For any of the 18 provider slots, "swapping the provider" is a selection, not
a source edit:

```sh
ggg provider set --provider ggg/mail:production=ggg/system/mail-smtp@smtp
ggg provider list --json
```

The command journals the change, re-resolves the graph, installs the new
adapter, drops the old one if nothing else selects it, and regenerates the
boot function for that environment. Nothing in your handlers changes: they
hold the seam's capability, not the adapter.

**Writing a new adapter** for an existing slot is the interesting case, and it
is a module like any other:

1. `ggg create provider NAME --slot SLOT --package internal/<seam>/<name>
   --constructor NewModule --definition NAME.json`. The definition supplies
   targets, inputs, environment keys, dependencies, lifecycle and health; the
   capabilities come from the slot itself, so an adapter cannot advertise a
   capability set the seam did not declare.
2. Implement the seam interface in that package. The vendor SDK is imported
   **there** and nowhere else, and every vendor env key is declared on that
   module.
3. Run the seam's contract suite against it — `internal/mail/contract.Run`,
   `runClientContract`, and so on. An adapter that passes a narrower table
   than the managed one is an adapter that will fail in one environment only.
4. `ggg provider set` it for the environment you want, then
   `ggg provider test --slot SLOT --environment ENV`.

Publishing that adapter to other projects, rather than keeping it local, is
the next section.

Two slots are worth knowing about before you start: `internal/identity` and
`internal/billing` still hold their webhook parsers (and therefore the Clerk
SDK, `svix` and `standard-webhooks`) in the **seam** package rather than in
the adapters. A new identity or billing adapter has to leave those files
alone and add its own; see [Roadmap](/docs/roadmap).

## B2C mode (no organizations)

`ggg/system/organizations` is `replacement-required`, so this is a fork of the
installed source rather than a removal.

1. `internal/web/auth.go`: drop `requireOrg` from the app chain (and the
   SelectOrg branch); make `loadPlan` resolve by user instead of org.
2. Migration: rekey `subscriptions`, `projects`, `api_tokens` and `audit_log`
   from `org_id` to `user_id`; update the queries to match. Both columns are
   already provider-neutral internal ids, so nothing about the identity
   adapter is involved.
3. Delete the org machinery: the membership webhook cases, `memberships.sql`,
   the SelectOrg page, Settings → Organization, and the organization switcher
   in the sidebar — each is a manifest file entry as well as a source file.
4. Checkout metadata carries the user id as the external id, and
   `billing_accounts` keys on that instead.

The identity webhook then only needs user events.

## Add an API endpoint

1. `internal/api/<resource>.go` — write the handler against the **same sqlc
   queries** the HTML handlers use. The API is a second transport, never
   parallel logic.
2. Declare the route in the owning workflow's `runtime.routes` with
   `"scope": "api-read"` or `"api-write"`; `RequireAPIToken` follows from the
   scope. Declare the matching operation under `openapi`, which is how
   `internal/api/openapi_registry_gen.yaml` is built — an orphan route or an
   orphan operation fails generation, so the description cannot drift from
   the routes.
3. Errors go through `api.WriteError` (`{"error":{"code","message"}}`); the
   plan limit is 402 with code `plan_limit`.
4. Mutations audit with `metadata {"via":"api"}`.
5. Versioning: `/api/v1` is **additive-only**; a breaking change means a new
   `/api/v2` mount. `ggg/system/api` is `major-version-only` for the same reason.
   See [API](/docs/api).
6. Test at the integration layer (`internal/web/api_test.go` covers token,
   scope, 402 and 404).

## Add an admin page

1. Handler in `internal/web/page_admin_<name>.go`.
2. A `runtime.routes` entry with `"scope": "admin"`; the admin chain already
   appends `requireStaff` and `requireAdminWrite`, and a mutation sets
   `"admin_write": true` in its policy.
3. Template in `internal/web/templates/admin_<name>.templ` plus a
   `runtime.navigation` entry in the `admin` area.
4. Test the negative case: non-admin → 403.

## Add a content type

Blog posts and changelog releases are two registered content types; a third is
one Go value plus one declaration. **No migration, no new table, no new
handler, no new template** — the admin list, the editor, validation,
revisions, publishing, the cache, the public index and detail routes, and
sitemap and RSS inclusion all follow from the declaration.

1. **Declare the type** — append a `content.Type` to `content.DefaultTypes()`
   in `internal/content/types.go` (owned by `ggg/system/content`):

   ```go
   {
   	Kind: "guide", LabelKey: "content.type.guide", PluralKey: "content.type.guides",
   	Path: "/guides", Mode: content.ModePages, Slug: content.SlugFromTitle, Sitemap: true,
   	Fields: []content.Field{{Key: "level", LabelKey: "content.field.level",
   		Kind: content.FieldSelect, Required: true, Options: []string{"intro", "advanced"}}},
   }
   ```

2. **Declare its routes** — a `runtime.content_types` record on the owning
   module: `{"id": "guide", "mode": "pages", "paths": ["/guides"],
   "package": "internal/web", "handler": "handleContent"}`. That is what
   generates the concrete `/guides` and `/guides/{slug}` routes, so the
   generator knows every reserved prefix before boot.
3. **Translate it** — `LabelKey`, `PluralKey`, every `Field.LabelKey`, and for
   a `FieldSelect` each `LabelKey + "_" + option`, in the owning manifest's
   `locales` for **both** `en` and `es`. Registry keys are read from Go
   values, so the template scanner cannot see them; a test over
   `content.DefaultTypes()` fails the build when a locale is missing one.
4. `ggg generate`, restart. `/admin/content` grows a Guides filter and a New
   button, `/admin/content/new?kind=guide` renders and validates the `level`
   select, and publishing puts an entry on `/guides` and `/guides/{slug}`
   through the generic templates — canonical and `hreflang` tags included,
   slug in `sitemap.xml`, absent from `/rss.xml` because the type does not set
   `Feed`.

Bespoke public markup is the one optional step: an entry in `contentViews()`
in `internal/web/handlers_content.go`, which is exactly what `post` and
`release` do to keep their existing pages.

`Path: ""` is a mode of its own. The type gets full admin CRUD, revisions and
programmatic reads with **no public route at all** — that is how in-app copy,
help snippets and legal blurbs are managed.

One limit, worth knowing before you design a type: type-specific fields live
in the **unindexed** `meta` JSONB. A type that must FILTER or SORT by one of
its fields has outgrown `meta` and should own a real column or a side table.
See [Content](/docs/content).

## Add a docs page

1. Create `content/docs/<slug>.md` with frontmatter `title`, `description`,
   `section`, `weight` (int) — plus `draft: true` to keep it out of
   production. The renderer supplies the `h1` from `title`, so the body starts
   at `##`.
2. Add the file to `ggg/system/content-assets`'s manifest `files` with
   `"class": "content"`, then `ggg registry build`. Every shipped
   docs page is module-owned; a page no manifest lists still renders in this
   repository but is not distributed, so `ggg sync` will never install it into
   a derivative.
3. Nothing else: the sidebar groups by `section` and orders by `weight`,
   `/docs` redirects to the lowest-weight page, and the page joins
   `sitemap.xml` automatically.
4. Restart the server (content parses at boot; air does this on save).
5. Cross-link freely, but only to real slugs, and give the page a `weight` no
   other page uses. `TestDocsInternalLinksResolve` fails the build on a dead
   `/docs/…` link and `TestDocsInventory` requires strictly increasing
   weights. See [Content](/docs/content).

Three docs pages are the exception, because they are generated from the
manifests and carry a `DO NOT EDIT` banner:
[configuration reference](/docs/configuration-reference) from the
`environment` records, [module reference](/docs/module-reference) from the
module graph, and [component reference](/docs/component-reference) from the
`runtime.ui` records. To change a row, change the declaration.

## Add an export

The CSV export dogfoods three batteries: a **job** (`export.projects_csv` —
enqueued from the handler, not scheduled), the **storage** seam (DevStore
works zero-account) and a **notification** carrying the download link. The
shape, from `ggg/workflow/project-export`:

1. Add the job kind, payload (`{OrgID, UserID}`) and `runtime.jobs`
   declaration.
2. Render with `encoding/csv`, then
   `storage.Put("exports/{org}/name-<ts>.csv")` → `InsertFile` row (it appears
   on `/app/files`) → `notify.Send(org, userID, "export.ready", …,
   "/app/files/{id}")`.
3. The handler enqueues and toasts "Export started"; the worker completes
   async. See [File storage](/docs/storage) for the seam.

## Add a locale

Locales are declared, not hand-merged. `internal/i18n/catalog_en_registry_gen.go`,
`catalog_es_registry_gen.go` and `locales_registry_gen.go` are all generated
from the `locales` blocks of every installed manifest.

1. Add the tag and its native label to `Locales` in `internal/i18n/i18n.go`
   (owned by `ggg/system/i18n`), and to the matcher list.
2. Add the new locale code to the `locales` block of **every** module that
   owns a key. Generation refuses a key that is missing from a declared locale
   and refuses two locales whose format placeholders disagree, so there is no
   way to ship a half-translated catalog.
3. `ggg generate`. See [Internationalization](/docs/i18n).

## Add a component

A component is a module: one public renderer, one options struct, one
directory in the registry.

1. **Check first.** `ggg catalog --kind component` and
   `--kind element` list 143 live modules, which between them export the 172
   renderers in `ui.ComponentRegistry`. `/dev/gallery` and the
   [component reference](/docs/component-reference) show them rendered.
2. **A new colour?** A token in the `@theme` block of `input.css`, and in
   `.dark` if it flips. Never a hex in a template.
3. **A recurring visual?** A class in `@layer components`. Group the base
   selector with its variants (`.btn, .btn-primary, …`) so a bare variant
   class still renders the component. No `dark:` in this layer — the tokens
   flip instead. A module may also own its own CSS fragment; the
   `@import` list in `input.css` is generated, so installing or removing a
   module never patches the entry file.
4. **A recurring structure?** A new `.templ` in
   `internal/web/templates/ui/`, exporting exactly one renderer of the shape
   `templ Name(o NameOpts)` where `NameOpts` embeds `ui.Attrs`. `ui` imports
   templ and stdlib only — never `templates`, `billing`, `identity` or sqlc —
   and the compiler enforces that direction. Reuse the closed enums (`Kind`,
   `Size`, `Emphasis`, …) and map domain values onto them next to their data.
   See [UI foundations](/docs/ui-foundations).
5. **An icon?** One const in `icons.templ` plus one switch arm emitting the
   complete `<svg>`. `TestIconRegistryIsComplete` fails a const with no arm.
6. **Declare it.** A `ggg/component/<kebab-name>` manifest requiring
   `ggg/element/ui-core`, with a `runtime.ui` record carrying `name`, `family`,
   `signature`, `summary` and `states`. That record is what puts the component
   on `/dev/gallery/{family}/{name}`, in the generated reference, and in the
   visual and axe sweeps. `TestGalleryCoversEveryInstalledComponent` compares
   installed metadata against rendered `data-ui` values, so an undeclared or
   ungalleried component fails the gate.
7. `ggg registry build && ggg generate && ggg check`, then
   `make visual-update` if the family baseline legitimately moved.

## Add a theme (rebrand)

`theme.local.css` at the repo root is the seam. It is yours: upstream never
writes to it, so `ggg update` can never conflict on it, and every token in
`input.css` can be replaced from it without touching `input.css` at all. That
is what keeps a rebranded project mergeable with the registry.

```css
/* theme.local.css */
:root {
  /* Ramp steps first: a few tokens are declared as var(--color-brand-N)
     rather than as a hex, so overriding only the aliases leaves them behind. */
  --color-brand-400:        #4ade80;
  --color-brand-500:        #22c55e;
  --color-brand-600:        #16a34a;
  --color-brand-950:        #052e16;

  --color-brand:            #16a34a;
  --color-brand-hover:      #15803d;
  --color-brand-text:       #15803d;
  --color-brand-subtle:     #f0fdf4;
  --color-brand-subtle-fg:  #14532d;
  --radius-control: 0.25rem;
}

.dark {
  --color-brand-text:      #86efac;
  --color-brand-subtle:    #052e16;
  --color-brand-subtle-fg: #86efac;
}
```

`make css`, reload: every primary button, link and focus ring is green in both
themes, and `input.css` is byte-identical to upstream.

The ramp steps are in that list for a reason. `--color-focus-ring` reads
`--color-brand-500` in light and `--color-brand-400` in dark, and `.dark`'s
brand text and tints read 400/950/300. Override only the semantic aliases and
those keep the old hue — which shows up as an indigo focus ring on a green
button.

**Why a rule near the top of the file still wins.** CSS requires every
`@import` to precede other statements, so the import of `theme.local.css`
cannot sit at the end of `input.css`. It does not have to. Tailwind emits its
tokens into `@layer theme` and its component classes into `@layer components`,
and *unlayered author CSS beats any layer at equal specificity*. A plain rule
in `theme.local.css` therefore overrides the boilerplate wherever it appears.

Two kinds of edit:

- **Override** a token or a component class: write a plain, unlayered rule.
  Every utility and every component class reads its colour through `var()`, so
  changing the variable restyles the whole product in both themes.
- **Add** a token that needs generated utilities — a `bg-accent` class that does
  not exist yet — inside `@theme { … }`. Tailwind only emits a utility for a
  name it knows about, so a plain `:root` declaration gives you
  `var(--accent)` and no `bg-accent`.

A **module CSS fragment** overrides the same way and by the same rule: an
unlayered rule in the fragment beats the boilerplate, and `@theme` /
`@layer components` blocks in it extend the system. The generated import list
(`internal/web/styles/modules_registry_gen.css`) loads before
`theme.local.css`, so the project always wins over a module.

### What the override file cannot reach

Three things live outside the stylesheet and still need editing for a full
rebrand:

1. **Email** — `emailStyle` in `internal/web/templates/theme.go`. Mail clients
   strip `<style>` and cannot read custom properties, so email is the one
   surface that inlines hex. `TestEmailStyleTracksTheLightThemeTokens` reads the
   light-mode value of each token out of `input.css` and fails with the field
   name when the two drift — but it compares against `input.css`, not against
   `theme.local.css`, so a rebrand done purely in the override file must still
   carry its new hexes into `emailStyle` by hand.
2. **Identity** — `BrandName`, `DocsEditBase` and the nav/footer lists in
   `internal/web/templates/chrome.go`; the logo mark is `IconLogo` in
   `icons.templ` and `static/favicon.svg`.
3. **Catalogs** — the product name appears inside translated prose
   (`email.footer`, `email.*.subject`), which lives in the owning manifests'
   `locales` blocks, not in a template variable: word order around a brand
   differs per language.

Then `ggg generate`, open `/dev/gallery` in both themes, and
`make visual-update` / `make visual` to prove the new baselines compare cleanly.

### Editing input.css instead (the fork path)

Changing `input.css` in place is still supported and is the right move when the
*system* changes rather than its values — a new token family, a new component
class, a different set of state kinds. It is a fork of a manifest-owned file, so
`ggg update` stages upstream changes to it under `tmp/ggg/conflicts/` for
`ggg resolve` instead of overwriting your edits. The order that matters there:

1. **Brand ramp** — all eleven `--color-brand-50` … `--color-brand-950` in the
   `@theme` block. The semantic aliases read 600/500/400 and the tints read
   50/950, so a partial swap leaves the tints on the old hue.
2. **Semantic aliases** — `--color-brand`, `-hover`, `-fg`, `-fg-muted`,
   `-text`, `-subtle`, `-subtle-fg`. Only if the mapping changes (a pale brand
   needs a dark `--color-brand-fg`).
3. **State triads** — `success` / `warn` / `danger` / `info` / `neutral`, six
   slots each, plus their `.dark` overrides. The `.k-*` matrix reads them, so
   every semantic family follows.
4. **Structural tokens** — `--container-page`, `--container-narrow`,
   `--spacing-sidebar`, `--spacing-docnav`, `--spacing-topbar`,
   `--spacing-navbar` if the shell proportions change.
