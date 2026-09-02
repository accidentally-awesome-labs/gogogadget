---
title: Extending GoGoGadget
description: The recipe hub — install, modify, author and update modules with ggg, then the per-feature recipes.
section: Guides
weight: 27
---

Every change follows the same shape: find the **module that owns the file**,
edit that module's source, regenerate, and let `sync` prove nothing drifted.

That last part is the difference from a plain boilerplate. The cross-cutting
files — the route table, the config struct, the i18n catalogs, the OpenAPI
description, `.env.example`, the seed data, the Playwright surface list — are
no longer places you edit. They are rendered from the module manifests by
`ggg sync`, so a hand edit there survives exactly until the next
`make generate` and then vanishes without a word. Change the declaration
instead and the generated file follows.

```sh
go run ./cmd/ggg info workflow/projects   # who owns what, and what to run
# …edit the module's own source…
make generate                             # ggg sync → templ → sqlc → tailwind
make check                                # the gate: generate + no-drift + vet + test + build
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
| **Generated output** | `*_templ.go`, every `*_registry_gen.*`, `internal/db/sqlc/`, `static/app.css`, `static/ui-components.js`, `static/ui-engines.js`, `.env.example`, `content/docs/configuration-reference.md`, `content/docs/module-reference.md`, `content/docs/component-reference.md`, `e2e/generated/personas.ts`, `e2e/generated/surfaces.ts`, `internal/web/templates/scenarios_gen.go`, `internal/web/templates/ui/reference_gen.go` | Tool-owned. Never edit; `ggg sync --check` fails on drift. |

That list is not a convention — it is `IsGeneratedOutputPath` in
`internal/modkit`, and the planner refuses to let a module claim any path it
matches.

## Install what already exists

This project selects 240 modules: 21 elements, 122 components, 36 pages,
28 workflows and 33 systems. `ggg catalog` lists 242 entries, because the two
this project excludes — `component/table-empty` and `element/divider` — stay
in the lock as `removed` tombstones so a later `ggg add` knows what they were.
Before writing anything, check whether what you need is already there.

```sh
go run ./cmd/ggg catalog                       # every module and its state
go run ./cmd/ggg catalog --kind component      # one kind
go run ./cmd/ggg catalog --installed           # only what this project has
go run ./cmd/ggg info component/data-table     # files, deps, links, verify commands
```

`ggg info` answers the two questions you have next — where can I look at this,
and what do I run — as `gallery` / `scenario` / `route` links and literal
`verify` commands. Add `--json` to any command for the machine envelope
(`{ok, command, run_id, registry_commit, resolved, changes, generated,
conflicts, diagnostics, exit}`).

```sh
go run ./cmd/ggg add component/kanban          # installs it and its dependency closure
go run ./cmd/ggg remove component/carousel     # after the removal checks below
```

`add` and `remove` edit **only** `gogogadget.json` and then run the same
reconciler `sync` runs; they are not a second code path. This project selects
`profile/full` and subtracts, so `add` deletes an id from `exclude` and
`remove` appends one:

```json
{
  "schema": 1,
  "registry": { "repository": "gogogadget/gogogadget", "ref": "main" },
  "modules": ["profile/full"],
  "exclude": ["component/table-empty", "element/divider"]
}
```

`gogogadget.lock.json` is the generated, committed counterpart: the resolved
registry commit, the deterministic dependency `order`, and per file a
`base_sha256` (what upstream shipped), a `local_sha256` (what is on disk) and
a `state` — `clean`, `modified`, `missing`, `conflicted` or `generated`. That
pair is what makes "did I change
this?" a question with an answer.

Every command exits with a declared code, so automation can branch on it:
`0` ok, `1` fetch/runtime error, `2` usage or schema error, `3` a preflight
refusal, `4` safe modules updated but conflicts remain staged, `5` the
transaction failed and was rolled back.

## Modify installed source

Installed source is ordinary source. Open the file, change it, regenerate.
Nothing has to be told, and there is no fork:

```console
$ go run ./cmd/ggg diff
modified   page/home                internal/web/templates/home.templ
modified   system/static            static/app.css
```

`diff` lists every file whose bytes differ from the locked base, per module.
`ggg diff KIND/NAME` narrows to one module; `--upstream` also lists clean
files and, when a conflict is staged, the path of its unified diff.

The one thing to keep straight: a local edit is invisible to the compiler but
loud to the updater. It never blocks you and it never gets clobbered — it
turns the next upstream change to that same file into a conflict you resolve
deliberately, which is the next section.

## Resolve an update conflict

```sh
go run ./cmd/ggg update            # advance the whole installed graph to one commit
```

Pristine files are replaced silently. A file you edited that upstream also
changed is **never** overwritten. Your bytes stay exactly as they are, the
complete upstream candidate and a unified diff are written under
`tmp/ggg/conflicts/<run>/<module>/`, the conflict is recorded in the lock, and
the command exits **4**. Independent modules that had no conflict still
advance; the conflicted module, its reverse dependents, and any dependency
whose `contract` also changed stay pinned at their old commit.

Read the diff, then pick one:

```sh
go run ./cmd/ggg resolve component/badge --path internal/web/templates/ui/badge.templ --accept-upstream
go run ./cmd/ggg resolve component/badge --path internal/web/templates/ui/badge.templ --keep-local
go run ./cmd/ggg resolve component/badge --path internal/web/templates/ui/badge.templ --merged
```

- `--accept-upstream` writes the candidate and records the file clean. Your
  edit is gone; you had the diff.
- `--merged` records the bytes currently on disk — merge by hand first — as
  `modified` against the **new** base.
- `--keep-local` keeps your bytes untouched but advances `base_sha256`,
  `revision`, `contract` and `source_commit` to the resolved upstream. The
  same conflict therefore clears for good, and the *next* upstream change to
  that file conflicts correctly instead of re-reporting this one.

Then `go run ./cmd/ggg sync` and the tree is green again. A conflict is
deliberately not portable: the candidate bytes live in ignored `tmp/`, so
`sync --check` keeps failing until someone resolves it and nobody can commit
a half-updated tree as a good state. If you clone a repo whose lock carries
conflict metadata but whose `tmp/` is empty, `ggg doctor` reports
`candidate_missing` and naming the modules; rerunning `ggg update` at the
lock's target commit re-materializes the candidates without touching your
source.

## Author a module

A module is one directory: `registry/modules/<kind>/<name>/module.json`, next
to the source files it claims. There is no code in a manifest — no hook, no
postinstall, no command array. It is data the generators read.

```jsonc
{
  "schema": 1,
  "module": {
    "id": "workflow/widgets", "kind": "workflow", "name": "widgets",
    "revision": 1, "contract": 1,
    "title": "Widget create, update and delete",
    "description": "…",
    "requires": ["system/database", "system/security", "system/server"],
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
  the installer rewrite the canonical Go import prefix into a derivative's
  own module path.
- **`requires`** — a hard dependency edge. It is what makes `add` install a
  closure and `remove` refuse while a dependent is present.
- **`runtime`** — the typed contributions: `routes`, `jobs`, `navigation`,
  `slots`, `ui`, `scenarios`, `queries`, `content_types`, `assets`,
  `janitors`, `visual`, `system`. Each generates real Go, so a wrong package
  or handler name is a compile error on a named generated line, not a
  mystery at boot.
- **`environment`** — one record per env key (`key`, `field`, `type`,
  `description`, plus `secret`, `default`, `required`, `production_required`).
  This is the only place an env key is declared: the config struct, its
  validation, `.env.example` and the
  [configuration reference](/docs/configuration-reference) all come from here.
- **`locales`** — `{"en": {...}, "es": {...}}`, inline in the manifest. Every
  key must exist in every declared locale with matching format placeholders,
  and two modules may not own the same key. Generation refuses otherwise.
- **`data`** — one record per table (`scope`, `export`, `account_delete`,
  `organization_delete`, `persisted_jobs`, …). The account and organization
  export collectors and the deletion order are generated from these, so a new
  stateful module cannot be silently left out of a GDPR export.
- **`removal_policy`** — `free`, `retain-data`, `drain-required`,
  `replacement-required` or `major-version-only`. See
  [module removal](/docs/module-removal).

Then build and validate:

```sh
go run ./cmd/ggg registry build       # rescan registry/, refresh payload digests, verify vendored bytes
go run ./cmd/ggg registry validate    # check the catalog, then prove the example lifecycle in a derivative
go run ./cmd/ggg sync --offline       # install into this tree and regenerate
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

```sh
ggg registry init --namespace acme --canonical-module example.com/acme/ggg-registry
ggg registry keygen --private registry-private-key.b64 --public registry-public-key.b64
ggg registry build  --dir .          # refresh digests, rebuild indexes, write the snapshot
ggg registry sign   --dir . --key-file registry-private-key.b64
ggg registry verify --dir . --public-key "$(cat registry-public-key.b64)"
ggg registry validate                # install, compile, test, remove, compare byte for byte
git tag -a v1.0.0 -m "acme registry v1.0.0"
```

`sign` accepts **exactly one** of `--key-file` or the base64
`GGG_REGISTRY_SIGNING_KEY` environment variable, and refuses when both or
neither are set; CI uses the environment form so the private key never touches
a disk. Publish the **public** key — it is the string consumers pin.

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

| Policy | Modules | Behaviour |
|---|---|---|
| `replacement-required` | `element/ui-core`, `system/apphost`, `system/config`, `system/database`, `system/i18n`, `system/modkit`, `system/organizations`, `system/security`, `system/server`, `system/static`, `workflow/appearance`, `workflow/auth-session` | Refused (exit 3). There is nothing left to run without them; swapping one is a manual migration. |
| `major-version-only` | `system/api`, `system/billing`, `system/identity`, `system/rate-limit`, `workflow/billing-webhook` | Refused (exit 3). `/api/v1` is a published contract and identity is every existing session; dropping either breaks live clients, so it belongs in a major version. |
| `drain-required` | `system/jobs` | Allowed only if the manifest supplies a reviewed forward **neutralization** migration that disables schedules and terminally marks persisted work before the new binary starts. `--purge-data` additionally requires a reviewed teardown migration. |
| `retain-data` | audit, content, notifications, schedules, storage, usage, webhooks, flags, announcements, impersonation, API tokens, blog, changelog | Removed, but their tables and rows stay. |
| `free` | everything else (209 modules) | Removed cleanly. |

Removal also refuses (exit 3, before touching anything) when a module is
required by an installed dependent, when one of its owned files is missing,
or when one is locally modified — the diagnostic names `ggg diff KIND/NAME`
and asks you to back the customization up or revert it deliberately. There is
no force flag.

**Advanced widget assets are optional, self-hosted and checksummed.** Three
modules vendor a browser engine, each recorded in its manifest with source
URL, version, exact byte count, SHA-256 and licence:

| Module | Artifact | Version | Bytes | Licence |
|---|---|---|---|---|
| `component/chart` | `static/vendor/chartjs-4.5.1.umd.min.js` | 4.5.1 | 208522 | MIT |
| `component/calendar` | `static/vendor/cally-0.9.2.js` | 0.9.2 | 38355 | MIT |
| `component/kanban` | `static/vendor/sortablejs-1.15.7.min.js` | 1.15.7 | 45478 | MIT |

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

`workflow/projects` plus `page/projects`, `page/project-new` and
`page/project-edit` are the worked example; read them with `ggg info` before
copying. A resource is normally **two modules**: a page module owning the
read surface and a workflow module owning the mutations.

1. **Migration** — `internal/db/migrations/00NN_widgets.sql` with
   `-- +goose Up` / `-- +goose Down`. Org-scope it
   (`clerk_org_id TEXT NOT NULL REFERENCES orgs(clerk_org_id) ON DELETE CASCADE`)
   plus `created_at` / `updated_at`. Declare it in the manifest's
   `migrations`; the number is allocated once and pinned in the lock. See
   [Database](/docs/database).
2. **Queries** — `internal/db/queries/widgets.sql`, one file per table. Every
   UPDATE sets `updated_at = now()`; every WHERE carries `clerk_org_id = $1`
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
10. `make generate && make check`.

## Add a plan

1. **Plan truth** — append to the `Plans` slice in
   `internal/billing/plans.go` (owned by `system/billing`). Order is render
   order; keep `free` first (`PlanByKey` falls back to index 0).
2. **Polar product** — create it in the Polar dashboard; copy the id.
3. **Env** — add an `environment` record to `system/billing`'s manifest
   (`"key": "POLAR_PRODUCT_BUSINESS", "field": "PolarProductBusiness",
   "type": "string"`). Do **not** touch `.env.example` or the config struct;
   both are generated from that record. Copy the new line into your own `.env`.
4. **Wiring** — extend `billing.SetPolarProductIDs` with the new case.

The pricing page, upgrade CTAs and usage meters render from `billing.Plans`
automatically. Enforcement (`MaxProjects`) applies the moment the plan exists.
See [Billing](/docs/billing).

## Add annual pricing

1. Add an `Interval` field (`"month"` / `"year"`) to `billing.Plan` and set it
   on each entry.
2. A second Polar product per paid plan, each with its own `environment`
   record, then extend `SetPolarProductIDs`.
3. Teach the webhook's product-id → plan-key reverse map that the annual ids
   map to the same keys, and persist the interval on the subscription row
   (new column, new migration).
4. Fix the math: `MRR` in `internal/billing/plans.go` divides annual
   subscriptions by 12.
5. Pricing page: group `ui.PlanCard`s by plan, toggle by interval.

## Add an email kind

1. **Templates** — the HTML and plain-text components in
   `internal/web/templates/emails.templ` (they share `EmailLayout`).
2. **Builder** — an `XMessage(locale, appURL, to, …) (mail.Message, error)`
   constructor in `internal/mail/mail.go`, next to `WelcomeMessage`. Bodies
   render to strings at enqueue time; workers never touch templates.
3. **Job kind** — a `Kind…` const plus a `jobs.Define` registration, and a
   `runtime.jobs` record on `system/mail`'s manifest:
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
3. `make generate`, then a db round-trip test: exact word, websearch
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
  `workflow/identity-webhook-sync`). Test with the `signSvix` fixture, which
  emits real `svix-*` headers.
- **Polar** — a `case` in `Processor.ProcessSubscription`
  (`internal/billing/webhook.go`), reached from
  `internal/web/workflow_billing_webhook.go`. Test with `signStandard`, which
  emits real `webhook-*` headers.

Idempotency (`webhook_events`) and the 400/500 retry semantics apply
automatically. Signature verification is not optional; the two header families
are explained in [Security](/docs/security).

## Add an OAuth provider

Clerk dashboard → SSO connections → enable Google/GitHub/…. **Zero code**: the
hosted Account Portal renders the buttons and the mirror sync does not care
how a user authenticated. 2FA is the same story. See
[Authentication](/docs/authentication).

## Swap the billing provider

`system/billing` is `major-version-only`, so this is a source edit inside the
installed module, not a `ggg remove`.

1. New file `internal/billing/<provider>.go` implementing `billing.Client`
   (`CreateCheckout`, `CreatePortalSession`, `RevokeSubscription`,
   `IngestUsage`) — `internal/billing/polar.go` is the template (raw
   `net/http`, no provider SDK). This is the only provider-touching file.
2. Replace verification and payload parsing in
   `internal/web/workflow_billing_webhook.go` and the `SubscriptionPayload`
   mapping in `internal/billing/webhook.go`. Keep the event → action table
   semantics: that state machine is the product, not the provider.
3. Swap the env declarations on the manifest, and construct the new client in
   `internal/billing/module.go`.
4. Handler tests survive untouched — they run against `billing.MockClient`,
   and the seam's `contract_test.go` suite runs the real client and the mock
   through the same table.

`plans.go`, entitlements and the pricing page are provider-agnostic and stay.
Your edits show up in `ggg diff` as `modified` and are never overwritten by
`update`.

## Swap the auth provider

1. Implement `identity.Verifier` — `Verify(ctx, token) (*Claims, error)` — in
   one file next to `internal/identity/verifier.go`.
2. Implement `identity.UserFetcher` for the lazy mirror upsert.
3. Construct both in `system/identity`'s module constructor.
4. Replace the mirror-sync webhook: Clerk-shaped parsing in
   `internal/identity/sync.go` and `handlers_webhooks.go` becomes your
   provider's delivery format. Keep the mirror schema (`users`, `orgs`,
   `org_members`) — everything downstream reads it.
5. Point the `/login`, `/signup`, `/logout` redirects in
   `internal/web/workflow_auth_session.go` at the new hosted UI.

Every guard (`RequireAuth`, `RequireOrg`, …) and the e2e `FakeVerifier`
continue to work unchanged. That is what the seam is for.

## B2C mode (no organizations)

`system/organizations` is `replacement-required`, so this is a fork of the
installed source rather than a removal.

1. `internal/web/auth.go`: drop `requireOrg` from `appChain` (and the
   SelectOrg branch); make `loadPlan` resolve by user instead of org.
2. Migration: rekey `subscriptions`, `projects`, `api_tokens` and `audit_log`
   from `clerk_org_id` to `clerk_user_id`; update the queries to match.
3. Delete the org machinery: the `organizationMembership.*` webhook cases,
   `memberships.sql`, the SelectOrg page, Settings → Organization, and the
   `OrganizationSwitcher` in the sidebar — each is a manifest file entry as
   well as a source file.
4. Checkout metadata carries the user id as the external id.

The Clerk webhook then only needs `user.*` events.

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
   `/api/v2` mount. `system/api` is `major-version-only` for the same reason.
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
   in `internal/content/types.go` (owned by `system/content`):

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
4. `make generate`, restart. `/admin/content` grows a Guides filter and a New
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
2. Add the file to `system/content-assets`'s manifest `files` with
   `"class": "content"`, then `go run ./cmd/ggg registry build`. Every shipped
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
shape, from `workflow/project-export`:

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
   (owned by `system/i18n`), and to the matcher list.
2. Add the new locale code to the `locales` block of **every** module that
   owns a key. Generation refuses a key that is missing from a declared locale
   and refuses two locales whose format placeholders disagree, so there is no
   way to ship a half-translated catalog.
3. `make generate`. See [Internationalization](/docs/i18n).

## Add a component

A component is a module: one public renderer, one options struct, one
directory in the registry.

1. **Check first.** `go run ./cmd/ggg catalog --kind component` and
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
6. **Declare it.** A `component/<kebab-name>` manifest requiring
   `element/ui-core`, with a `runtime.ui` record carrying `name`, `family`,
   `signature`, `summary` and `states`. That record is what puts the component
   on `/dev/gallery/{family}/{name}`, in the generated reference, and in the
   visual and axe sweeps. `TestGalleryCoversEveryInstalledComponent` compares
   installed metadata against rendered `data-ui` values, so an undeclared or
   ungalleried component fails the gate.
7. `go run ./cmd/ggg registry build && make generate && make check`, then
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

Then `make generate`, open `/dev/gallery` in both themes, and
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
