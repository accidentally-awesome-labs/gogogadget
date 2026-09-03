# Task E report — recast README and the hand-owned docs

Commit: **fbf2526** `docs: recast the README and hand-owned pages around the framework workflows`
(worktree `/Users/salar/Projects/gogogadget/.worktrees/framework-followups`, branch
`framework-followups`, on top of 6788f83). Working tree clean.

## What each page now says, and what it replaced

### `README.md` (full rewrite)
**Was:** "The production-grade Go + HTMX SaaS boilerplate… wired to managed
services", a Clerk/Polar/Resend/R2/PostHog/Sentry block diagram, `make setup &&
docker compose up -d db && make seed && make dev`, "240 modules", a features
list written as a product brochure.
**Now:** an opinionated framework shipped as a source-module registry. The
public promise is the first thing on the page, in the required order. Verified
content: `ggg new` quick start (with `/tmp/ggg setup` building `bin/ggg`, then
`bin/ggg services up|db migrate|db seed|dev`), the full 18-slot table with
development/test and production defaults and the other targets, `ggg provider
set|list|configure|test`, the module/CLI section with real counts, `ggg create`,
the managed-service key table keyed by slot, `.ggg/env/<env>.env` discipline,
`ggg deployment set` + `ggg deploy`, and the docs index.

### `content/docs/getting-started.md` (full rewrite)
**Was:** clone → `make setup` → `docker compose up -d db` → `make seed` →
`make dev`, plus "connect real services by filling `.env`".
**Now:** `ggg new` (registry forms, `--answers`, `--non-interactive`, ref
defaulting), `ggg init` for adoption, the five-profile table with member and
required-slot counts, then `setup` → `services up` → `db migrate` → `db seed` →
`dev` with what each actually runs. Zero-account mode kept and made accurate
(the local adapter per slot, `DEV_AUTH_BYPASS`, `/dev/login`, the production
boot refusal). Managed-service section is now `provider list/set/configure/test`
with the real validation and refusal semantics.

### `content/docs/architecture.md` (full rewrite)
**Was:** a package table, a middleware chain that still listed `recover`, Clerk
context keys, "three generated trees".
**Now:** the five layers (manifests → resolver → journalled apply → generated
bootstrap → provider slots), the schema-2 intent shape, the lock's provenance
ledgers, the `Config.Env` boot switch and `providerActive` gating, the health
contract with its real numbers (2s deadline, 10s cache, three critical slots),
the seam/adapter/handler rule **with its three real exceptions**, an updated
package map, the actual middleware order, declared routes, provider-neutral
context keys, and both generated-output families.

### `content/docs/extending.md` (targeted recast, recipes preserved)
Replaced: intro, boundary table (added `compose.yaml`/`compose.test.yaml` and
`e2e/generated/inventory.ts`), the install section (schema-2 intent JSON, real
counts, scoped ids, the exact-slot-set rule), `diff`/`update`/`resolve` examples
(including the two targeted-update forms), the manifest example and every field
note (requirement objects, `dependencies`, `runtime.system`/adapter/slot,
`environment.targets`, `claims`), the removal-policy table (scoped ids, real
per-policy counts), and the three stale recipes: "Swap the billing provider" and
"Swap the auth provider" became one **"Swap a provider"** section (selection,
then how to write an adapter), and the B2C recipe now names `org_id`/`user_id`
instead of `clerk_org_id`/`clerk_user_id`.
Added: **"Create source with `ggg create`"** — every form, what it emits, the
`create resource` slice, the three refusals and the two plan-visible narrowings.
Preserved verbatim: the task C "Publish your own registry" section.
Mechanical: every backticked unscoped module id scoped to `ggg/…` (unscoped ids
are now rejected — verified: `ggg info workflow/projects` → `module id
"workflow/projects" is invalid`), `go run ./cmd/ggg` → `ggg`, `make generate`/
`make check` → `ggg generate`/`ggg check`.

### `content/docs/deployment.md` (full rewrite)
**Was:** Dockerfile stages, `fly secrets set …` with a hardcoded Clerk key list,
a Neon section, an env checklist, scaling caveats.
**Now:** `ggg deployment set`, the two deployment modules and what each drives,
the six deploy verbs with their real confirmation behaviour, stale-plan refusal,
`--resume RUN_ID` and partial-failure resume, the remote envelope grammar, the
four secrets rules, Fly and Docker (corrected Dockerfile stages — it calls
templ/sqlc/tailwind directly, not `ggg generate`), generated compose files,
`ggg services`, db backup/restore/restore-drill, `/healthz` vs `/readyz` plus
the honest note that `Runtime.Health` is not what `/readyz` reads, and scaling
as a provider selection.

### `content/docs/testing.md` (targeted)
Kept task B's e2e-ownership section and task D's CI section unchanged in
substance. Changed: five layers instead of four with a **Contract** row and a
new Contract section naming the real runners; `ggg test`/`ggg check` as the
entry points; `ggg services up` instead of `docker compose up -d db`; the CI
`git diff --exit-code -- ':!gogogadget.lock.json'` correction; replaced the
duplicate/stale "Seam contract suites" section with **"The provider permutation
gate"** (the two `registry/testdata` provider fixtures plus the external one);
corrected the fuzz section — `make fuzz` runs only `FuzzFakeVerifier`.

### `content/docs/security.md` (full rewrite)
**Was:** CSP/CSRF/rate-limit/webhooks/XSS, Clerk-specific throughout.
**Now:** the real middleware order (no `recover`), the real CSP (including
`worker-src 'self' blob:` and the `Permissions-Policy` without
`interest-cohort`), CSRF, **provider-neutral identity ids** with the three
mapping tables and `ErrLinkRequired`/`ggg identity link`, webhook verification
(both families, handlers SDK-free), SSRF, rate limiting as a fail-closed slot,
API tokens, **credential discipline** (five rules), **supply chain** (signing,
rotation, snapshot pinning, inert installation, contributed-CLI limits, vendored
assets, declared dependencies), staff roles, the fire-and-forget quartet vs the
transactional audit row, exports, XSS/body caps, the dev backdoor, GDPR.

### `content/docs/roadmap.md` (full rewrite)
**Was:** a 2026-08-17 point-in-time SaaS-feature audit.
**Now:** three tables — **Shipped** (the framework surface, one row per
capability), **Known gaps** (nine, each with the file that proves it), and
**Delegated** — plus a short "Not started".

### Out-of-scope pages: false sentences fixed (not rewritten)
- `content/docs/index.md` — was "a full-featured SaaS boilerplate… Clone it,
  run `make setup && make dev`". Recast as the docs index: the promise in order,
  the stack table, and a section table linking **every** page (verified: every
  slug except `index` itself is linked).
- `content/docs/cli.md` — schema-1 intent JSON → schema-2; "There is
  deliberately no `--help` text… the authoritative list is in
  `internal/modkit/cli.go`" (that file no longer exists) → help and completions
  derived from `CommandTable()` in `internal/gggcli/table.go`; command table and
  flag notes replaced with the shipped surface; `242 rows / 240 modules` → 288
  live; "eleven keys" → ten (the envelope has ten fields).
- `content/docs/modules.md` — `{"schema": 1, …}` → 2, the `ggg/component/badge`
  example given its scoped id and requirement-object `requires` plus
  `dependencies`, "Seventeen fields are required" → Eighteen (verified against
  `$defs.Manifest.required` in `registry/schema/module.schema.json`).

## Code the claims were verified against

| Claim area | Source read |
|---|---|
| Every command, subcommand, flag, default | `internal/gggcli/table.go:51-170`, `lookupProviderAction` in `remote_handlers.go:172-201`, and `/tmp/ggg-docs help` output |
| `ggg new` answers, ref defaulting, mutual exclusion | `task8_handlers.go:16-122`, `new_project.go:20-214` |
| `ggg create` forms and refusals | `create.go:36-198`, `create_resource.go:145-342` |
| Trusted tasks (setup/generate/services/dev/db/check/test/build) | `tasks.go:61-300` |
| Provider list/configure/provision/test | `remote_exec.go:648-694`, `applyProviderConfigure`, `parseConfigureValues`, `validateTargetInput` |
| Deploy verbs, confirm gate, stale refusal, resume | `remote_handlers.go:238-281`, `remote.go:243-259`, `remote_exec.go:411-523` |
| Doctor findings and `--fix` | `remote_exec2.go:368-619` |
| Env/state discipline | `internal/remote/env.go`, `internal/remote/state.go` |
| Registry authoring, signing, rotation | `registry_authors.go:63-191`, `internal/modkit/registry_lifecycle.go`, `snapshot.go` |
| Closure families and counts | `internal/modkit/example.go:180-308,450-511`, `providerFixtureSpecFor` |
| Schema 2 model, lock, envelope, exit codes | `internal/modkit/model.go`, `plan.go:43-96`, `contract.go` |
| Generated boot, `providerActive`, health | `internal/modkit/generate.go:569-596`, `internal/modules/bootstrap_registry_gen.go`, `internal/apphost/apphost.go:120-242` |
| Generated-path predicates | `IsGeneratedOutputPath` / `IsRegistryOwnedOutputPath` |
| Slots, adapters, targets, criticality, inputs | every `registry/modules/system/*/module.json` (jq sweep) |
| Profiles | `registry/profiles/*.json` |
| Middleware, CSP, rate limit, readyz, dev login | `internal/web/{server.go,middleware.go,auth.go,workflow_dev_session.go}` |
| Seam vendor imports | `go list -f '{{.Imports}}'` over all 16 seam packages |
| Deploy targets | `internal/deploy/{fly,docker}/target.go`, `internal/database/ops/docker` |
| CI, Makefile, Dockerfile, compose | `.github/workflows/ci.yml`, `Makefile`, `Dockerfile`, `compose*.yaml` |
| Counts | `/tmp/ggg-docs catalog --json` (297 rows: 288 clean, 5 removed, 4 available), `gogogadget.lock.json` reason/policy aggregation |

## Code/doc mismatches found — left for the parent

All are **code** defects, not doc defects. Each is recorded in
`content/docs/roadmap.md` under "Known gaps" so the shipped docs stay honest.

1. **Panic recovery is not installed.** `(*Server).recover` exists at
   `internal/web/middleware.go:59` and its own comment at line 24 documents the
   chain including it, but `Server.Handler()` no longer wraps it — removed in
   54b8b33 (`git show 54b8b33 -- internal/web/server.go` shows `- h = s.recover(h)`).
   A handler panic therefore escapes to net/http's default handler: no 500 page,
   no `observability.Reporter` capture. `renderError`'s "from the recover
   middleware" comment and `server.go:226`'s chain comment are both now stale.
   **This is the most serious finding.**
2. **`Runtime.Health` has no consumer.** The generated report is complete and
   `critical` is wired, but `handleReadyz` (`internal/web/server.go:283`) is
   still a bare `db.Ping`. `grep` finds no caller of `Runtime.Health` outside
   tests. The plan's "a critical slot's outage blocks `/readyz`" is not true today.
3. **`ggg deploy apply` cannot run noninteractively.** `runDeploy`
   (`remote_handlers.go:265-268`) passes `needsConfirm: true` unconditionally, so
   `confirmRemote` refuses a non-TTY run and refuses `--json` *even with `--yes`*
   (with the misleading message "noninteractive JSON requires --yes"). `rollback`
   and `secrets` correctly go through `requireRemoteConfirm`.
4. **`ggg deploy plan` is an alias of `status`** (`remote_handlers.go:254-257`
   both dispatch `DeployStatusRequest`, and the envelope reports
   `command: "deploy status"`). There is no way to see a change set without
   starting an apply.
5. **Payload-only read commands print nothing in human mode.** `ggg provider
   list`, `provider test` and `deploy status` return `Result.Payload` with an
   empty-change envelope, and `renderHuman` only renders envelope fields —
   verified by running `/tmp/ggg-docs provider list` (no output, exit 0). Docs
   now say "use `--json`".
6. **Two seams still carry vendor SDKs.** `internal/identity` imports
   `clerk-sdk-go` and `svix`; `internal/billing` imports `standard-webhooks`.
   `ggg/system/identity`'s manifest declares both Go dependencies while
   `ggg/system/identity-clerk` declares none. Plan step 5's "adapter modules own
   their signature header families" is not finished.
7. **`make fuzz` runs one of two fuzz targets** (`FuzzSanitizeFilename` in
   `internal/mail/dev` is never invoked).
8. **`ggg doctor --fix` implements one remediation** (`env_file_missing`).
9. Brief nit: the brief says "19 provider slots"; the registry declares **18**
   (jq sweep over `runtime.provider_slots`). Docs say 18.

## Link-resolution check

Extended the existing mechanism in `internal/content/docs_check_test.go` rather
than adding a second one:

- `docsLinkRe` widened from `\]\(/docs/([a-z0-9-]+)\)` to
  `\]\(/docs/([a-z0-9-]+)[/#)]`, so `/docs/security#csrf` and `/docs/security/`
  are now checked too — previously invisible.
- New `TestReadmeDocsLinksResolve` reads `../../README.md` (the one docs file
  the content package does not embed, and the first thing a reader opens) and
  asserts every `](content/docs/<slug>.md)` link exists on disk. Repo-relative
  reads in tests have precedent (`internal/db/seed_registry_test.go`,
  `internal/web/templates/designsystem_test.go`).

## Commands run

```
$ go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline
registry 719575f235470e798edd427c948784aea937656029053b6af85fb63dd57a5e43
  update    lock       gogogadget.lock.json

$ go run ./cmd/ggg sync --check --offline
registry 719575f235470e798edd427c948784aea937656029053b6af85fb63dd57a5e43
exit=0

$ git diff --stat -- content/docs/{configuration,module,component}-reference.md
(no output — the three generated reference pages are byte-identical)

$ go test ./internal/content ./internal/web -count=1
ok  github.com/gogogadget/gogogadget/internal/content  3.926s
ok  github.com/gogogadget/gogogadget/internal/web     85.478s
```

Also run for verification only (read-only): `ggg catalog --json`, `ggg help`,
`ggg info ggg/workflow/projects`, `ggg info workflow/projects` (refusal),
`ggg doctor`, `ggg diff`, `ggg provider list [--json]`.

Not run, per the brief: `make check`, e2e, visual, any formatter or linter.

## Concerns

- **The panic-recovery gap (finding 1) should be fixed before this branch
  lands.** It is a live availability and observability regression, and the
  roadmap entry is a stopgap, not a fix. Once `s.recover(h)` is restored, the
  middleware chains in `architecture.md` and `security.md` both need `recover`
  inserted after `MaxBytesReader` and the roadmap row deleted.
- Findings 2–5 are user-visible surface gaps. If any is fixed, the matching
  sentence in `deployment.md` (apply confirmation, `plan` aliasing, `/readyz`)
  or `cli.md`/`getting-started.md` (`--json` for read commands) must move with
  it.
- `content/docs/{api,billing,authentication,email,storage,database,
  feature-flags,observability,configuration,troubleshooting}.md` were out of
  scope and are still written provider-first (Clerk/Polar/Resend/R2 as facts
  rather than as one selectable target). They contain no statement I found to be
  outright false about a workflow this program changed, but they read as
  pre-framework and are the obvious follow-up batch.
- `internal/web` tests take ~85s; if the parent's gate is time-boxed, budget for
  it.

---

# Fix round 1 — response to `task-e-review-findings.md`

Commit: **5dfecf9**, on top of e281317 (which already carried the sibling's
`dce9e7e` contract fixes and its own doc updates to architecture/deployment/
roadmap/testing).

## C1 — the two billing recipes, rewritten against the shipped API

`extending.md` "Add a plan" and "Add annual pricing" named three things that do
not exist. Re-derived every step from code:

| Was | Is |
|---|---|
| "append to the `Plans` slice" | append to `defaultPlans` (`internal/billing/plans.go:28`, unexported); catalog reached through `DefaultPlanCatalog()` |
| "`PlanByKey` falls back to index 0" | `planCatalog.ByKey` falls back to the `free` entry (`catalog.go:52-57`); `FreePlan()` reads `defaultPlans[0]` |
| "add the env record to `ggg/system/billing`'s manifest" | add it to **`ggg/system/billing-polar`** — all five Polar keys are declared there, and the seam declares none |
| "extend `billing.SetPolarProductIDs`" (symbol deleted) | extend the `switch p.Key` in `internal/billing/polar/module.go:33-41`, then `billing.NewPlanCatalog` |
| "surfaces render from `billing.Plans`" | they render from the injected `billing.PlanCatalog`; the webhook's product-id → plan-key map is built from `catalog.All()` (`workflow_billing_webhook.go:62`) |
| — (missing) | `billinglocal.LocalPlanCatalog` uses each plan's key as its product id, so a new paid plan is checkout-able in dev/test with no adapter work |
| "`MRR` divides annual by 12" | `MRRWithCatalog` is the function to change |

Added the `NewPlanCatalog` refusal set (empty catalog, empty key, duplicate key,
duplicate non-empty product id) so a step-1/step-4 mistake is a named boot
error.

Same dead symbol found and fixed one page over: `billing.md:32-38` claimed
"`Plans` is an ordered slice… at boot, `SetPolarProductIDs` injects…". Rewritten
against `defaultPlans` / `PlanCatalog.ByKey` / the two adapters' catalogs.

## I1 — README default table now names one profile and matches it

The table is now stated as `ggg/profile/full`'s seeds, with a sentence naming
how `minimal` differs. Two rows were wrong for **any** profile reading and are
corrected against `registry/profiles/full.json`: `ggg/realtime` production is
`realtime-ably@ably` (was `realtime-postgres@postgres`) and `ggg/observability`
production is `observability-sentry@sentry` (was `observability-log@log`).
Verified with jq that `full`'s `test` column is identical to `development` for
all 18 slots, which the header now says.

## I2 — the Neon keys

`ggg/database` / Neon now reads `DATABASE_URL`, `NEON_API_KEY`, optional
`NEON_PROJECT_ID`. `NEON_API_KEY` is `required: true` on the target's inputs and
enforced by the generated validator (`config_registry_gen.go:208-210`). Also
added optional `POLAR_SERVER`, which the adapter declares and the table omitted.

## I3 — the joined-error claim, made accurate in four places

`README.md`, `content/docs/index.md`, `getting-started.md` and
`architecture.md` now say: nothing falls back silently, **and** there are two
mechanisms. The generated validator joins the `production_required` keys into
one error — verified as exactly `DATABASE_URL`, `NEON_API_KEY`,
`RESEND_API_KEY`, four `STORAGE_R2_*` (from the generated `is required` checks)
and four `CLERK_*` (from `requireProductionKeys`), all collected through
`errors.Join` at `internal/config/config.go:71`. Polar, PostHog, Sentry and the
OpenAI-compatible LLM adapter validate inside their own constructors, so those
fail on the first one reached.

Related manifest defect fixed: `POLAR_ACCESS_TOKEN`'s description in
`registry/modules/system/billing-polar/module.json` no longer claims a 503
not-configured fallback. Applied as a one-line text edit (a JSON round-trip
would have reordered the whole file), then regenerated — the new text
propagated to `.env.example` and `content/docs/configuration-reference.md` and
nothing else moved.

## Minor

- **M1** — `full`'s gloss now says "every product module in the catalog" and
  explains why `saas` (296) is larger: `full` leaves the nine
  environment-selected adapter modules to the provider selections. Verified by
  diffing the catalog against `full.json`'s members: exactly 11 absent — the
  nine mail/storage/identity/billing adapters plus the two this repo excludes.
- **M2** — `security.md`'s chain now includes `recover` in its restored
  position (inside the telemetry span, outside every named middleware), with a
  sentence on why that position matters. The "panic detail only outside
  production" line is true again as a result.
- **M3** — already correct: the sibling's commit rewrote `deployment.md`'s
  health paragraph and `/readyz` row. Re-read against `handleReadyz` and
  confirmed critical ⇒ 503 naming the slot, non-critical ⇒ 200 degraded, names
  only.
- **M4** — `modules.md`'s five kind examples are now scoped ids.
- **M5** — `testing.md` now says `visual.sh` execs `visual-run.sh compare`
  (verified: `scripts/visual.sh` is exactly that two-line wrapper). Used a bold
  cross-reference rather than an in-page anchor, since the docs renderer's
  heading ids are not part of the link contract the test checks.

## Six-gap sections re-read against the shipped behaviour

`architecture.md`, `deployment.md`, `roadmap.md` and `testing.md` were already
updated by `dce9e7e`; I re-read each against the code rather than trusting the
diff, and they are precise: the `deploy_plan` payload (`plan_hash`,
`observed_state_hash`, ordered `deploy://<id>/<resource>` changes, secret key
names only) matches `executeDeployPlan`; the uniform `--yes`/`--resume`
confirmation matches `runDeploy:266-276`; `/readyz` matches `handleReadyz`;
`make fuzz` matches the two-line target. `roadmap.md`'s gap list is down to four
rows and none of the six remain. The one place still describing old behaviour
was `getting-started.md`'s `provider list --json`, which now shows the human
form too (verified against a real run: `provider list --slot ggg/mail` prints
three rows).

## Commands run

```
$ go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline
registry 9dbfdb35d1a4da3e67f102b8a085ab58db4d463f9f46abc7cab2c8e93b7f29e6
  update    lock       gogogadget.lock.json

$ go run ./cmd/ggg sync --check --offline
check_exit=0

$ git diff --stat -- content/docs/{module,component}-reference.md
(no output — unchanged; configuration-reference.md and .env.example changed by
exactly the one regenerated POLAR_ACCESS_TOKEN description)

$ go test ./internal/content -count=1
ok  github.com/gogogadget/gogogadget/internal/content  3.263s

$ /tmp/ggg-docs2 provider list --slot ggg/mail
ggg/mail  development  ggg/system/mail-dev@filesystem     development/manual
ggg/mail  test         ggg/system/mail-dev@filesystem     development/manual
ggg/mail  production   ggg/system/mail-resend@resend      managed/manual
```

`go vet ./internal/content ./internal/billing/...` clean. `make check`, e2e and
visual still not run, per the brief.

## Remaining concern

`roadmap.md` keeps two rows for the identity/billing vendor-SDK gap — one for
the seam source imports, one for the seam manifests' declared Go dependencies.
They are genuinely two fixes (move the webhook parsers; then move the
`dependencies.go` entries), so both stay until both land.
