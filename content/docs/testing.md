---
title: Testing
description: The layer decision rule, the contract suites every adapter runs, the gates, and the fixtures that make them deterministic.
section: Guides
weight: 23
---

Five layers, one decision rule:

| Layer       | Runs on                                   | Write it for                                                        |
|-------------|-------------------------------------------|---------------------------------------------------------------------|
| Unit        | nothing (no DB)                           | pure logic — entitlements, config, plans, token hashing             |
| Contract    | an `httptest` fake of the provider's wire | one seam's behaviour, run by **every** adapter of that slot         |
| Integration | real Postgres (`TEST_DATABASE_URL`)       | handler/route behavior — webhooks, guards, limits, API auth         |
| End-to-end  | Playwright (real server + DB + browser)   | user flows — HTMX CRUD, redirects, toasts                           |
| Visual      | Playwright screenshots (dockerized)       | pixel-level layout, in both themes                                  |

**Pure logic → unit. A seam's behaviour → contract. Handler behavior →
integration. User flow → e2e. Pixels → visual.** Never reach for a heavier
layer than the behavior needs.

The five layers above are a **decision rule**, not a list of commands.
`ggg test` runs one mode at a time — `integration`, `e2e`, `visual`, `smoke`,
or `all` — and `ggg check` is the commit gate: generate → stale
generated-output refusal → drift check (`sync --check --offline`) → `go vet` →
the accounted `go test` → `go build`. The `make` targets are thin aliases over
`bin/ggg`.

There is no `ggg test unit`, and it is worth knowing why, because there was
one until recently: it ran the identical `go test ./...` over the identical
packages as `integration`, database fixtures included, while this page
described it as running no database. Go's test granularity is the package and
this repository's database-touching packages hold pure tests in the same
package — `internal/web` carries `designsystem_test.go` beside its integration
server tests — so a `unit` mode that excluded database-dependent packages
would drop real coverage rather than describe the truth. The name is a usage
error (exit 2) naming `integration`, not a silent alias.

### A skipped test is not a passing test {#skips}

`go test` never summarises skips. A package whose every fixture skipped prints
the same `ok` as one that ran, and that is exactly how it went wrong: the test
stack was torn down between rounds, `internal/audit`, `internal/notify`,
`internal/schedules` and `internal/usage` each skipped 100% of their tests,
all four printed `ok`, and "targeted tests pass" was reported on that basis.
386 tests skipped in that run out of 1,895.

So the gate accounts for what the suite did. `ggg check` and `ggg test
integration` read the `go test -json` event stream and always print

```
tests: 1927 passed, 0 skipped, 0 inapplicable, 0 failed across 91 packages
```

naming every package that skipped anything, and marking a package that ran
nothing at all with `NO TEST RAN: all N skipped`. The counts are **leaf
tests**: `go test -json` reports a verdict for a parent and for each of its
subtests, so summing every event would give a number that means pass *events*
under a label that says tests.

Three refusals sit on top of it:

- **a nonzero skip count when `CI` is set.** CI's `test` job names
  `TEST_DATABASE_URL` in its own `env:` block and runs the service container
  that answers on it, so a skip there is a test that had everything it asked
  for.
- **a run that executed nothing.** `go test ./...` against a tree matching no
  packages exits 0 with a warning, so an account of zero packages or zero
  tests is refused rather than printed as a clean sheet.
- **stale generated output**, described under [the CLI](/docs/cli).

`ggg test` takes `--race` and `--cover`, which is how CI runs this gate
instead of its own bare `go test`. The run is always `-count=1`: Go's test
cache keys on inputs it can observe, and a database that stopped answering is
not one, so a cached entry replays a previous run's skip count.

Both flags belong to the Go layer and reach only it. `ggg test all --race`
hands them to the `integration` child and leaves the e2e, visual and smoke
argv untouched — `--race` is not a flag `npx playwright test` or a shell
script understands. It used to be accepted by `all` and then dropped before
the one child that could honour it, which reads as race coverage nobody has;
naming a flag and ignoring it is worse than refusing it. On any other single
mode a non-zero flag set is still a usage error naming `integration`.

#### Declaring a skip inapplicable {#inapplicable}

A skip is sometimes correct and permanent. `internal/config`'s derivation
tests skip when the project's `test` environment publishes no local Postgres,
which is exactly what a project on a managed database (Neon) does: there is
nothing to supply and the skip is right. Refusing it in CI would be a false
refusal, and the only remaining move would be deleting the test.

So a skip may declare itself. Put `[inapplicable]` in the skip message:

```go
t.Skip("[inapplicable] the test environment publishes no local Postgres; nothing to derive")
```

Those are counted separately, printed as `N inapplicable`, and never refused.
It is a declaration at the skip site, not a heuristic — the gate cannot tell an
absent service from an inapplicable case by looking, so only the test may say.

Where the inapplicability is known before the subtest starts, **not
registering the case is still better**: an unregistered case is a smaller
table, while a marked skip is still a test that did not run. That is what
`internal/billing/contract` does, and it asserts its omission set (see
[Contract](#contract) below) so a silently shrinking table
fails.

`internal/db/testdb` draws the matching line between **absent** and
**broken**. `TEST_DATABASE_URL` being set is a request — somebody named a
server — so one that does not answer is a failure. A merely *derived* address
is not a request: it is what this project's test stack would publish, equally
true whether or not the stack is running, so a contributor who never ran
`bin/ggg services up --environment test` gets a skip and the unit layer. "Is
it set" and "is it defaulted" are different questions, and only the first
means anyone asked for a database.

Which layer a given change needs is answerable without guessing. Every module
declares its test inventory, and `ggg info` prints it as commands you can run
verbatim:

```console
$ ggg info ggg/workflow/projects
…
  verify   go test -count=1 ./internal/web
```

Go packages become `go test -count=1 ./<pkg>`, declared e2e and accessibility
specs become `cd e2e && npx playwright test <spec> --reporter=line`, and a
declared visual surface becomes `./scripts/visual.sh` — which is a two-line
wrapper that execs `scripts/visual-run.sh compare`, the harness described
under **Visual** below — never a plain `playwright test`, because baselines
only match inside the pinned container.

One rule cuts across every layer: **a test payload never names an adapter
package.** A seam ships its own double — `identity.MockVerifier`,
`mail.MockSender`, `storage.NewMockStore()`, `billing.MockClient`,
`ratelimit.NewMockLimiter` — and tests hold that, because an adapter is a
per-environment provider selection that no `requires` can express.
`ggg sync` refuses a plan that breaks it. See
[Extending → A seam ships its own test double](/docs/extending#a-seam-ships-its-own-test-double).

## What a derivative runs {#what-a-derivative-runs}

`ggg check` is the same gate in your project as it is in this repository, with
one declared exception. A handful of payloads assert about the repository that
**publishes** the registry — that its committed snapshot verifies under the
pinned core key, that its example and external fixtures are digest-exact, that
its CI workflows exercise every closure family, that its vendored bytes match
their declarations, that every file it tracks has exactly one owning module.
Those are declared `self_host: true` in their manifest (see
[Modules → Files](/docs/modules#files)) and the installer skips them unless the
project's `go.mod` module path is the registry's `canonical_module`. They are
still fetched and digest-verified, so a tampered one refuses everywhere; they
are never installed, and never run, in a generated project.

That is a subtraction, not a weakening. Those assertions need this
repository's `registry/testdata`, `registry/external-testdata`,
`templates/external-registry`, `registry/schema`, `.github/workflows` and git
index — artifacts a derivative does not have and should not carry. Everything
that tests the engine, the CLI, the seams and your own source ships and runs
normally, including the registry, planner, apply, removal, vendor and
ownership tests that are portable. Three guards keep the line honest:
`TestSelfHostPayloadsAreDeclaredAndPresent` (the set is non-empty and every
member is really in the tree), `TestCoreRepositoryInstallsEverySelfHostPayload`
(the publishing repository still installs and runs all of them), and
`TestSelfHostPayloadsInstallOnlyIntoThePublishingRepository` (the planner
installs them there and nowhere else) — the last of which is portable, so your
project checks the rule too.

## Unit

Plain `go test`, no database. Examples: the `Entitled` status matrix, plan
limits and MRR math, config validation, the `e2e:` token parser, the JSON
error shape.

Unit is a **layer**, and the layer is what the decision rule above is about:
write a pure test when the behaviour is pure. It is not a command. The one
Go-test mode is `ggg test integration`, which runs every package including
these, because `go test` cannot address a layer — it addresses packages, and
pure tests live beside database-backed ones in the same package.

## Contract

Every provider seam owns one behaviour suite that **every** adapter of that
slot runs. A local adapter that drifts from the managed one fails the same
table the managed one passes, which is what makes selecting a different
adapter per environment safe rather than hopeful.

Two shapes are in use, both owned by the seam:

- An exported runner, for a seam whose adapters live in their own packages —
  `internal/mail/contract.Run(t, factory)` called by
  `internal/mail/{dev,resend,smtp}/contract_test.go`, and
  `internal/storage/contract.Run`/`RunWithOptions` called by
  `internal/storage/{filesystem,s3}/contract_test.go`.
- An unexported table beside the seam — `runVerifierContract` (identity),
  `runClientContract` (billing), `runStoreContract` (storage),
  `runReporterContract` (observability) — each run once per implementation,
  the real client and the local one.

The real client is exercised against an `httptest` fake of the provider's own
wire format, so a mock cannot drift from the contract it stands in for.
Adapter lifecycle is part of it: a buffering adapter must flush inside the
caller's deadline and its `Stop` must be idempotent, and every adapter that
declares `health` satisfies `apphost.HealthChecker` — the generated bootstrap
emits a compile-time assertion, so a declaration without an implementation is
a build failure, not a runtime nil.

A case an implementation cannot reach is **not registered**, and the set of
omissions is **declared and asserted**. `billing/contract.RunClient` takes the
expected omissions as trailing arguments: the hosted Polar adapter passes none
(every method does real HTTP, so every provider-error case must run), and
`internal/billinglocal` declares all four, because that adapter has no failure
mode at all — `IngestUsage` is `return nil`. Registering a case and skipping it
was the old shape; it executed nothing either way and inflated the gate's skip
count. Declaring the set is what makes a *shrinking* table fail: drop an error
hook and the omissions no longer match the declaration.

## Integration

`internal/db/testdb` gives **every package its own database**
(`gogogadget_test_<name>`), dropped, recreated, and migrated at `Open` —
`go test ./...` runs packages in parallel, and a shared database would let
one package's teardown nuke another's fixtures. Tests self-skip only when
**nobody named a server**: an unreachable `TEST_DATABASE_URL` is a failure,
while the address derived from the test stack this project declares is an
absence, so `bin/ggg services up --environment test` is what turns the layer
on locally and CI names the server explicitly. See
[A skipped test is not a passing test](#skips).

**`TEST_DB_SUFFIX` is appended to that name.** The name is otherwise fixed per
package and `Open` drops before it creates, so two runs of the same package
against one server interleave a drop with a create and fail with errors that
name neither the test nor the cause (`duplicate key value violates unique
constraint pg_database_datname_index`, or a connection terminated by
administrator command) — in whichever package happened to be running. Give
each concurrent worker its own suffix and they get their own databases on the
same server. It defaults to empty, so a single run keeps the stable name, and
reusing one suffix per worker recycles databases instead of accumulating them.

Helpers in `internal/web/testhelpers_test.go`:

- `integrationServer` — a real `Server` against real Postgres, wired with
  the `FakeVerifier` (`DEV_AUTH_BYPASS`) auth path, so every guard and
  middleware executes for real.
- `serve` — issues a request through the full middleware stack.
- `sessionCookie(userID, orgID, role)` — builds a synthetic `__session`
  cookie.

Webhook fixtures mirror the two real header families exactly:

- `signSvix` emits `svix-id` / `svix-timestamp` / `svix-signature` — the
  Clerk delivery family.
- `signStandard` emits `webhook-id` / `webhook-timestamp` /
  `webhook-signature` — the Polar family (same signing scheme, different
  header names).

Production verification rejects the wrong family outright; the fixtures exist
so tests can't drift from that reality.

**A test never observes a server goroutine's state unsynchronised.** Two
intermittent failures in one day were the same shape: the test read something
the handler's goroutine was still writing — `httptest.ResponseRecorder.Body`
in one, a plain `int` counter in the other. Both passed locally and failed
under `-race`, which is why `-race` is the gate. The recorder case is refused
statically rather than left to luck: `modkit.ValidateNoRecorderGoroutineHandoff`
scans every `_test.go` payload on every plan and refuses a
`httptest.ResponseRecorder` that crosses a `go` statement, because that type
has no synchronisation of any kind and a streaming handler never stops writing
it. A test that genuinely needs a recorder on another goroutine passes one
whose `Write` and reader take the same mutex — `flushRecorder` is the shape.
The rule is the handoff, not the read, because proving a happens-before edge
to every write is exactly the reasoning that was got wrong. The broad version
of the same idea — any local written inside a goroutine or handler closure and
read outside it — was measured before it was rejected: it matches 30 sites in
this tree and 28 of them are the ordinary `httptest.NewServer` fixture that
`-race` proves clean on every run, so it would refuse correct code 93% of the
time and re-implement the race detector badly.

## End-to-end

Playwright (Chromium) drives the real server on **port 18080** — never the
dev port 8080, so the suite cannot attach to a stray dev server. The
`webServer` boots `go run ./cmd/server` with `APP_ENV=test`,
`DEV_AUTH_BYPASS=true`, a placeholder `CLERK_PORTAL_URL`, and
`TEST_NOW=2026-01-15T00:00:00Z`, and blank `CLERK_PUBLISHABLE_KEY` /
`CLERK_SECRET_KEY` — the server auto-loads `.env` in development, and a real
dev key would boot clerk-js and contradict the suite's own "no third-party
request" assertions. `globalSetup` reseeds the disposable test database on
every run via `go run ./cmd/seed -reset -registry e2e`, which loads the
module-owned fixtures under `internal/db/testdata/seed/e2e/`.

That database is **derived, not written down**: `e2e/generated/database.ts` is
rendered by `ggg sync` from the test environment's selected database adapter
and its effective host port, so the suite reaches the stack `ggg test e2e`
brings up (`localhost:15432`) rather than a literal that drifts from it.
Export `E2E_DATABASE_URL` to override, which is how CI names its own service
container.

`ggg test e2e` and `ggg test visual` bring that stack up with the app service
**scaled to zero**, because both own the app process themselves — Playwright's
`webServer` starts one on `:18080`, and the visual harness builds and runs one
for container mode. The stack's `app` service points at the same database, and
it is not idle just because nothing reaches it over HTTP: it runs its own jobs
worker, scheduler and audit exporter. Two workers claim from one
`FOR UPDATE SKIP LOCKED` queue while writing to two separate object stores, so
an export CSV lands in whichever process won the claim and the other answers
500 for a `files` row both of them can see. That was `export.spec.ts` failing a
third of its runs, invisible behind `retries:2`. `ggg services up` and
`ggg db reset` still bring the whole stack up, app service included: standing
the stack up in docker is the point of the first, and the second restores
exactly what it tore down.

`-reset` drops that database (`WITH (FORCE)`, so a stale connection cannot
block the reset). One disposable database per server is deliberate; the visual
harness resets the same one.

Login is a cookie the **server** mints, not a hosted page and not a string the
harness builds:

```ts
const context = await loginAs(browser, 'pro');
// GET /dev/session?user=user_pro&org=org_pro&role=org:admin → 204 + Set-Cookie
```

The personas are generated data. `e2e/generated/personas.ts` is rendered from
the `personas` declarations of the installed modules and exports `PersonaId` —
`free`, `pro`, `admin`, `support`, `disabled`, `noorg`, `noactive`, `toggle`,
`deleteme` — plus the `personas` array of triples. `e2e/helpers.ts` imports it
rather than keeping a second literal map, so a module that adds a persona
cannot leave the helper behind. Each persona owns disjoint orgs and rows, so
`fullyParallel` specs never mutate each other's fixtures.

What the harness does **not** have is the token's grammar. That belongs to
whichever identity adapter the environment selected — `e2e:<user>:<org>:<role>`
for `identity-dev` — and it is written in exactly one place, that adapter's
`MintSession`. It used to be restated in TypeScript too, by a generated
`sessionFor` helper, and nothing held the two spellings together: a grammar
change in Go kept every Go package green and failed only in the browser job,
as a bounce to `/login` with no diagnostic anywhere. The copy was not even
faithful, since `MintSession` refuses a subject containing the grammar's own
separator and the template did not.

So `loginAs` asks `GET /dev/session` (owned by `ggg/workflow/dev-session`,
gated by the same `DEV_AUTH_BYPASS` predicate `/dev/login` carries) and lets
`context.request`'s shared cookie jar carry the reply's `Set-Cookie` into
every page the context opens. `TestNoTypeScriptCanBuildASessionToken` holds
the invariant mechanically: no `.ts` file may name the session cookie or
install one a client assembled.

The refusal reaches the harness too, which is the point of routing it through
the server. A project selecting a **hosted** identity adapter for its test
environment has no minter, the route answers 503 naming
`identity.SyntheticSessionMinter`, and `loginAs` throws with that message
instead of carrying a cookie nothing verifies. A subject the adapter will not
mint is a 400 naming the subject — a caller error, deliberately not the same
answer as a missing capability.

Every spec belongs to the module whose surface it drives, declared in that
module's `files` and `tests.e2e`. `ggg/workflow/billing-checkout` brings
`e2e/billing.spec.ts`; `ggg/workflow/admin-flags` brings
`e2e/admin-flags.spec.ts`. A profile that installs no billing therefore
installs no billing spec.

Ownership splits three ways:

- `ggg/system/e2e` is the **harness** — `playwright.config.ts`,
  `global-setup.ts`, `helpers.ts`, `package.json`, `package-lock.json` — and
  requires `ggg/system/project-base` plus `ggg/workflow/dev-session`, whose
  mint route is how `loginAs` gets a cookie. Every spec-owning module requires
  the harness, because every spec imports `@playwright/test` and `./helpers`.
- `ggg/system/e2e-sweeps` owns the nine cross-cutting suites (accessibility,
  keyboard, progressive enhancement, loading, mobile, CSP, visual, public
  site) and `visual.spec.ts-snapshots/`. They assert shell and platform
  behaviour rather than one feature, so they stay together — and the module
  requires every page they sweep.
- every other spec belongs to its feature module.

That rule is mechanical, not aspirational.
`TestEveryDeclaredE2ESpecIsReachableFromItsOwner` extracts the literal
navigation targets from every declared spec — `page.goto('…')`, the `request`
verbs, and the string arrays a `for (const path of […])` loop walks — and
resolves each method and app/admin/public path against the same route table the
router is generated from. If the declaring module cannot reach the module
serving that route, through itself or its transitive `requires`, the spec is an
orphan — a derivative would install the test and 404 on a page nobody
installed — and the test fails naming the spec, the path and the owner.
`TestEveryE2ESpecOnDiskHasExactlyOneOwner` adds the other two halves: exactly
one owner per spec, and every owner must reach the harness.

Three things the gate deliberately does not see, so a green run is not read as
more than it is: **click navigation** (`getByRole('link', …).click()` and the
`toHaveURL`/`waitForURL` it lands on), **computed targets** (`surface.path`
from the generated inventory, template literals with a substitution), and
**adapter-served paths** such as billing-local's `/app/billing/confirm`, where
which adapter is active is a per-environment project decision. A few `requires`
edges exist for the first case and are not defended by the test.

Splitting a spec along module lines is the normal fix for an orphan; declaring
the missing `requires` is the other. One exception is declared rather than
fixed: `auth.spec.ts` (owned by `ggg/workflow/auth-session`) clicks through to
`/app/settings/account`, and `ggg/page/settings-account` requires
`auth-session` back, so the edge cannot be added without a dependency cycle.

Assertion discipline, by convention:

- retrying assertions only — `await expect(locator).toBeVisible()`, never a
  bare `isVisible()`;
- two selector axes, not one: **role and accessible name** for anything that
  is a semantic claim (`getByRole('button', { name: … })`), and
  `[data-testid]` for stable container and state identity. Never visible copy
  alone, which changes with locale and with copy edits;
- beware that Playwright's `name` option is a **substring** match unless you
  pass `exact: true`. An accessible name that merely contains another
  landmark's name resolves to two elements and trips strict mode in a spec
  nobody touched, so `grep` `e2e/` for the existing prefix before adding a
  landmark or `aria-label`;
- shell scripts run `set -euo pipefail`, so a failed step inside a pipe fails
  the run.

`retries` is 2 under `CI` and 0 locally, and the run **reports every test that
passed only on retry** — the name, the location, and which attempt finally
passed — as the last line the gate prints:

```
retried passes: 1 test failed at least once and was retried into a green run:
  [chromium] export produces a downloadable CSV (export.spec.ts:7) — passed on attempt 2 of 3
```

A clean run says `retried passes: none (retry budget 2)`, and a run with no
budget says so rather than implying nothing was retried. The reporter is
`e2e/retried-pass-reporter.ts`, registered last so its verdict is not scrolled
away.

It does not fail the build. A retried pass is a lead to investigate, and a
gate that fails on one gets its retries deleted instead of its flake fixed —
but a green exit code was previously the whole of what the gate said, so a test
failing a third of the time was indistinguishable from one that never failed.
That is how `export.spec.ts` hid a storage-topology defect: 33% at
`--workers=1 --retries=0`, ~3.6% and green with retries applied.

Some specs defend htmx invariants that no unit test can reach, because they only
exist in a live browser. Each was written by breaking the behaviour first and
watching the test fail:

| Spec | Invariant |
|---|---|
| `auth.spec.ts` | a navigation swaps only `#content`: a node appended to `<body>` (where clerk-js portals live) and the widget mount roots survive; Back/Forward re-fetch does not nest a page inside `#content` |
| `projects.spec.ts` | a mutation soft-navigates — the JS context and body-level nodes survive, so nothing re-mounts; `innerMorph` keeps a surviving row's DOM node (`isConnected` on a held handle) |
| `public.spec.ts` | the docs table of contents arrives and leaves with the page; an in-page anchor fires **zero** requests; a navigation lands at the top |

A regression test that passes against the broken implementation is decoration.
When one of these fails, read it as a design report: the chrome diverged, the
swap widened, or a link got boosted that shouldn't be.

Run the suite with `make e2e` (interactive mode: `make e2e-ui`).

**When a spec times out, measure before you cap anything.** The suite runs
`fullyParallel` on Playwright's default `workers` (half the logical cores — 5
on a 10-core machine) across two projects, and the heaviest specs already
carry per-case budgets: `a11y-states.spec.ts` sets
`30_000 × scans + 50_000 when a vendor bundle is awaited`, because a case that
walks a widget's states needs a budget proportional to its axe passes.
Measured on a 10-core M1 Max with the whole file running: the tightest
interaction case used **32.8%** of its budget at load average 16, and **35.0%**
at load average 143 — sixteen pure-CPU spinners and twenty-six containers, and
the wall clock did not move (2m32s against 2m40s). The suite itself draws 3.6
of 10 cores.

So an `a11y-states` timeout is not "the machine was busy": load average moves
the numbers by single percentage points, and capping workers would cost half
the wall clock on every good run to recover nothing. Eleven of these specs did
once time out at 30s, and a run that slow is 3× off a distribution that CPU
saturation cannot produce — look for the shared substrate instead (the Docker
VM the server, database and browsers all sit behind), and re-measure rather
than re-run.

## Visual

Screenshot specs cover the generated surface list in light AND dark.
`e2e/generated/surfaces.ts` is rendered by `ggg sync` from the component,
scenario and page manifests, so a new module brings its own baseline instead
of relying on someone remembering to add one: ten `family-*` gallery pages
(full-page, desktop), twelve `scenario-*` pages (desktop and mobile) and the
production pages each module declares under `runtime.visual`, with their own
persona and mask list.

Baselines are font-rendering-sensitive, so both commands run inside the
pinned Playwright Linux container:

```sh
make visual          # compare against the committed baselines — read-only
make visual-update   # the only thing allowed to overwrite a committed screenshot
```

`make visual` goes through `ggg test visual`, which brings the **test stack**
up itself — dependency services only, app service scaled to zero, because the
harness runs the server. It used to be a documented prerequisite
(`ggg services up --environment test`), which brought the app service with it
and put a second jobs worker beside the one the harness starts. Both go
through
`scripts/visual-run.sh`, which extracts the `@playwright/test` version from
`e2e/package.json`, resets the database `VISUAL_DATABASE_URL` names — empty
falls through to the project's derived test-stack address, and the value is
passed explicitly to the seed and the server so an ambient `DATABASE_URL`
cannot redirect a run that DROPS a database —
starts the host server on `:18080` with `APP_ENV=test`, `DEV_AUTH_BYPASS=true`,
`TEST_NOW=2026-01-15T00:00:00Z` and blank `CLERK_*`, then runs
`mcr.microsoft.com/playwright:v<version>-jammy` with
`--add-host host.docker.internal:host-gateway` and `E2E_NO_WEBSERVER=1`.
Only `update` adds `--update-snapshots`, and it writes to
`e2e/visual.spec.ts-snapshots/`. macOS screenshots diff by design — never
commit locally generated baselines. Determinism comes from `TEST_NOW`: under
`APP_ENV=test` the render clock freezes (`Config.Now()`), so every rendered
date and relative time is stable, and the e2e seed uses fixed `2026-01-15`
timestamps to match.

That clock **formats**; it never decides state. Anything a stored column and a
SQL predicate already answer — whether a content entry is live, scheduled or
expired — is computed in the query off the database's `now()`, never compared
against `Config.Now()` in a template. The two clocks are eight months apart
under the harness, and a badge that arbitrates with the frozen one calls a
live entry "Scheduled" while the public page serves it.

The gallery baselines are the highest-leverage ones: a shade, spacing or
variant regression anywhere in the component layer shows up as one named
failing screenshot instead of leaking into a page nobody captures.
`a11y.spec.ts` sweeps the same generated surfaces with axe in both themes,
`a11y-states.spec.ts` opens dialogs, menus, popovers and pickers before
scanning, and `keyboard.spec.ts` drives focus return, roving tabindex and the
no-JS fallbacks. These are dev-only routes (`DEV_AUTH_BYPASS`), which the
visual and e2e harnesses both set.

## CI

Eight jobs. `test` sets up Go and Postgres, then runs the gate: `make setup` →
`make generate` → `git diff --exit-code -- ':!gogogadget.lock.json'`
(generated code is committed and fresh, which is also what proves no registry
drift) → `go vet` → `govulncheck` → `bin/ggg test integration --race --cover`
→ `make fuzz` → `go build`. The test step goes through `ggg` rather than a
bare `go test -race -cover ./...` for one reason: `ggg` reads the event stream
and **refuses a nonzero skip count**, and this is the job where nothing can
legitimately be absent.
`e2e`, `visual`, `smoke`, `docker`, `registry-core`, `registry-external` and
`profiles`
all depend on `test`: `e2e` installs Chromium and runs `npx playwright test`
in `e2e/` (not `make e2e` — CI brings up no compose stack); `visual`
runs `make visual`, which owns its own seeding and host server — do not add
seed or start-server steps beside it, because a second process cannot bind
`:18080` and the baselines only reproduce against the harness that wrote them;
`smoke` boots the built binary against the CI Postgres and drives
`scripts/smoke.sh`; `docker` builds the image to catch Dockerfile drift. Both
`e2e` and `visual` upload their Playwright report on failure — a red visual
job without the expected/actual/diff images is unactionable. There is
deliberately no numeric coverage gate: a hard threshold punishes you for
deleting sample code.

`registry-core` runs `bin/ggg registry validate --closures core` and
`registry-external` runs `bin/ggg registry validate --closures external`.
That command is the only gate that proves a module can be installed,
generated, compiled, tested, removed, and the tree restored byte for byte;
everything else in the registry engine checks data. The families are two jobs
rather than one because they are two claims — the fixture registry under
`registry/testdata` (10 closures) versus the signed third-party tree under
`templates/external-registry` (1 closure) — so a signing or provenance
regression cannot be reported as a core fixture failure. Neither needs
Postgres: each closure lives in a throwaway derivative and touches no
database. The derivative path is stable per repository *and per family*, so
the two runs never share a work directory, a warm build cache, or the pid lock
that refuses a second concurrent run. `TestCIExercisesEveryClosureFamilyForReal`
asserts both jobs exist, are not gated or continue-on-error, and still have
closures to exercise.

`profiles` is the third row of the profile gate matrix, and the only one that
runs a real `ggg new`. Every shipped profile is created from the repository as
a directory registry and the created tree is then checked with
`sync --check --offline`, because exit 0 from genesis is not the claim: the
first thing an operator does in a new tree is a sync, and a genesis whose own
engine reports drift over the tree it just wrote has not produced a project.
It also compares the installed module count against the number that profile's
own description advertises, which is the number
`TestTheDocumentedProfileTableMatchesWhatTheProfilesResolveTo` checks against
the *planner* — so the cheap row and the expensive row are one claim.

Measured at ~22 s per profile, ~100 s for the four including the binary
build. It is a separate job for the same reason the two registry-validate
jobs are, and it is not the clock: genesis runs `go mod tidy` in a
destination outside this module's tree and installs the pinned Tailwind
binary, so the sweep needs the network, and `make check` has to stay runnable
offline. The test therefore skips as `[inapplicable]` unless
`GGG_GENESIS_SWEEP=1`, which only this job sets, and
`TestTheProfileSweepCIJobRunsThisTest` parses the workflow so that deleting
the job cannot leave the sweep green-by-skip everywhere.

The era walk is the fourth tier, one rung further out: it proves the
cross-release upgrade promise itself — a derivative created by an old
release's own binary walks forward to the current one with local edits
preserved byte for byte through a staged conflict. It materializes two era
trees with `git archive` (`v0.1.1`, the earliest schema-2 genesis, and
`v0.16.0`, the last era the pre-v0.24 authoring rules bricked), builds each
era's binary, creates a derivative per that era's documented flow, makes one
local edit in a module whose payload churns, and drives the whole
exit-4 → `resolve --keep-local` → completing-update sequence with the current
binary, then asserts provenance over the walked lock and a green
`sync --check --offline`. Measured ~165 s warm on an M1 Max, minutes
cold-cache, and it needs the network for the era-flow genesis, so it can
never be a contributor gate: it skips as `[inapplicable]` unless
`GGG_ERA_WALK=1`, which only the separate `era-walk` workflow sets —
`workflow_dispatch` plus one weekly schedule, never push or pull_request,
in no needs chain, never required — and `TestCIEraWalkWorkflowRunsTheEraWalk`
parses that workflow so deleting it cannot leave the walk green-by-skip.

The other two rows are in `go test` and therefore in `make check`:
`TestEveryShippedProfileResolvesIntoACoherentProject` plans each profile and
asserts two coherence properties over the planned bytes — every package a
planned payload imports from the project's own module path is a directory
something in the plan writes, and every table a planned migration alters or
references is created by some planned migration — and
`TestTheDocumentedProfileTableMatchesWhatTheProfilesResolveTo` pins the
documented table, the descriptions and the slot counts. All three rows walk
`catalog.Profiles`, so a fifth profile is covered without an edit. Only
`saas` was ever exercised end to end before this matrix existed, which is how
three profiles that could not create a project shipped for a release.

See [Database](/docs/database) for `TEST_DATABASE_URL` mechanics and
[Frontend](/docs/frontend) for the `data-testid` contract.

## The provider permutation gate

A seam with two adapters is two claims, and `ggg registry validate` is what
proves both. Beside the eight single-module example closures,
`registry/testdata` publishes two **provider fixtures** —
`fixture/system/mail-providers` and `fixture/system/storage-providers` —
each installing a seam plus two candidate adapters. The harness installs the
closure, switches the environment selection between the candidates,
recompiles, removes it, and compares the derivative tree byte for byte. The
external family adds the same shape for a third-party slot adapter
(`gadgetworks/system/audit-export-ledger` against `ggg/audit-export`).

That is what makes "development chooses the local adapter, production chooses
the managed one" a tested statement rather than a configuration convention.

## Provider verification: two tiers, neither a contributor gate for its credentials

Every managed adapter's package test drives a fake. That fake encodes a
**belief** about a provider's wire shape — Resend's `POST /emails` body and
its `{id}` response, Polar's `Polar-Version: 2026-04` header, Clerk's JWKS
document, the presigned-GET signature parameters R2 must honour. The belief is
what `make check` verifies; the provider is free to change under it at any
time, and when it does, every fake stays green and only a derivative in
production finds out.

So provider verification is two tiers. Tier 1 is the contributor gate and
stays one; tier 2 is the check on the belief itself. **Neither tier's
credential-bearing or container-bearing half is ever a required check for a
contributor**, and both refuse to pass vacuously: each skips with a stated,
reasoned `[inapplicable]` line naming exactly what is missing and the one
assignment that supplies it.

### Tier 1 — local protocol containers

Where a provider's protocol has a zero-account implementation, the adapter is
driven against a real server rather than an in-process fake. Two run today:
MinIO for `ggg/system/storage-s3` (the same adapter and the same path-style
SigV4 code that talks to R2, pointed at `STORAGE_S3_ENDPOINT`) and Mailpit for
`ggg/system/mail-smtp`, whose HTTP read-back API on port 8025 gives the test an
assertion an in-process fake cannot make — that the message really arrived, and
with the headers the adapter set. Both images are digest-pinned in their own
manifests and both are already declared `local_service` blocks, so selecting
those targets emits them into the generated Compose files.

The tests follow the repository's one service-dependency convention
(`internal/db/testdb`): an explicitly named but unreachable server **fails**,
while a merely derived one **skips**. They run in CI's existing `test` job
beside Postgres, so tier 1 costs a contributor nothing locally and is a real
gate in CI. Each test's own skip reason carries its measured warm cost.

No protocol container exists for the rest. Clerk, Polar, Resend, Ably,
LaunchDarkly, Knock, Svix and OpenMeter have no zero-account server, and the
two Redis adapters speak the Upstash REST dialect rather than RESP, so a plain
Valkey container cannot serve them without a REST shim.

### Tier 2 — the managed-target live canaries

`TestManagedTargetLiveCanaries` in `internal/canary` drives the real
maintained managed targets with live credentials and asserts the same wire
shapes the fakes assert. It is a declarative table — one row per managed
adapter, carrying the slot, the module, the target, the credential keys, a
cleanup class, the prose for what a run leaves behind, and the probe — so
**adding a provider is a row, not a file**.
`TestEveryManagedAdapterIsCanariedOrExcused` walks the registry for every
module publishing a `managed` service target and refuses one that appears in
neither the table nor the stated `liveCanaryNothingToProbe` allowlist, in both
directions, so a new managed adapter cannot be forgotten and a stale excuse
cannot outlive its reason.

It runs only from the separate `live-canary` workflow —
`workflow_dispatch` plus one weekly schedule, never push or pull_request, in
no needs chain, never required — which is the only thing that sets
`GGG_LIVE_CANARY=1`. `TestCILiveCanaryWorkflowIsNotAContributorGate` parses
that workflow so narrowing it to `push`, adding a `needs` edge, gating the
step, or repointing `-run` fails by name;
`TestEveryLiveCanaryKeyIsMappedInTheWorkflow` checks the other direction, that
every key a row declares is actually mapped there, because an unmapped key
would make a new row skip in CI forever while reporting a clean line.

Weekly rather than per-push for one reason: the failure this catches is a
**provider** moving, which no change to this repository can trigger. Per-push
would pay every provider's latency and spend for an answer that cannot have
changed; dispatch-only would mean it runs when someone remembers.

Three safety rules are enforced, not trusted:

1. **Cleanup is declared per row.** Self-cleaning rows remove what they
   created through the seam and verify the removal. Rows that cannot must
   cite, with a file and line, the seam declaration that has no delete — and
   the table refuses a row that does not.
2. **No probe points at production.** A row declares the selectors that would
   aim it at a production tenant and the suite **refuses the run** — a
   failure, not a skip. `POLAR_SERVER=production` is the one that exists:
   the Polar probe creates a checkout session and ingests an immutable metered
   event. The workflow additionally pins the literal `sandbox`, so a
   misconfigured repository secret cannot even reach the refusal.
3. **No failure prints a credential.** Probes return errors rather than
   failing directly, and the one reporting path scrubs every credential value
   the row declares, plus any userinfo inside one. A failing row names the
   provider and the endpoint and not the secret. One bound is worth stating:
   a transport-level DNS error can still echo the *hostname* of a credential
   that is itself a URL, because the host is the endpoint the report exists to
   name; the path, the token and the userinfo do not survive.

### What each canary leaves behind

Read this before configuring a secret. The suite is pointed at throwaway
accounts by design.

| Slot / adapter | Cleans up? | What a run leaves |
|---|---|---|
| `ggg/storage` — `storage-s3@r2` | yes | nothing; the object is deleted and the deletion verified |
| `ggg/cache` — `cache-redis@upstash` | yes | nothing; the key is deleted, and a failed run's key expires within a minute |
| `ggg/search` — `search-typesense` | yes | nothing; the document is deleted. The collection is operator-owned and never created or dropped |
| `ggg/llm` — `llm-openai-compatible` | n/a | no resource — but real tokens, on every run, in the account's usage record |
| `ggg/identity` — `identity-clerk` | n/a | nothing; every call is a read. The fixture user is deliberately **not** deleted |
| `ggg/database` — `database-postgres@neon` | n/a | nothing; only the provisioner's read-only `Check` is called, never `Plan` or `Apply` |
| `ggg/billing` — `billing-polar` | bounded | one checkout session per run, and at most **one** metered event ever, because the probe uses a stable `ExternalID` that Polar deduplicates on. Sandbox only |
| `ggg/rate-limit` — `rate-limit-redis@upstash` | bounded | one counter key, stable and self-expiring within the window the adapter sets |
| `ggg/usage` — `usage-openmeter` | bounded | one usage event on `CANARY_OPENMETER_SUBJECT`, and only ever one: the stable external id is carried as the CloudEvents id and OpenMeter deduplicates on `(source, id)`. Use a throwaway customer — a metered event on a real one reaches an invoice |
| `ggg/mail` — `mail-resend` | **no** | one real email per run to `CANARY_MAIL_TO`, against the account's quota. `mail.Sender` has only `Send`; there is no recall |
| `ggg/analytics` — `analytics-posthog` | **no** | one immutable event per run under a stable distinct id. Use a throwaway project |
| `ggg/observability` — `observability-sentry` | **no** | one immutable error event per run, against the project's quota and its alert rules. Use a throwaway project |
| `ggg/realtime` — `realtime-ably` | **no** | one channel message per run, retained if the channel persists history |
| `ggg/telemetry` — `telemetry-otlp` | **no** | one exported span per run, under the collector's retention |
| `ggg/audit-export` — `audit-export-otlp` | **no** | one exported audit entry per run. The transactional Postgres audit row is untouched and can never be bypassed |
| `ggg/notifications` — `notifications-knock` | **no** | one real workflow run per run, delivered to `CANARY_KNOCK_RECIPIENT` through whatever channels the canary workflow enables, against the account's quota. `notifications.Notifier` has only `Send`/`SendOrg`, and the adapter sends no cancellation key |
| `ggg/webhooks` — `webhooks-svix` | **no** | one real Svix message per run on `CANARY_SVIX_APP`, fanned out to every endpoint that application subscribes, retained for the account's payload-retention window. Use a throwaway application |

### The operator's secret checklist

Every key is optional. An unset repository secret expands to the empty string,
so the row skips with its reason instead of failing — configure the providers
you care about and leave the rest. The env variable each secret feeds is the
adapter's own declared key, so the mapping is the adapter's contract, not the
canary's invention.

| Repository secret | Feeds |
|---|---|
| `CANARY_RESEND_API_KEY`, `CANARY_MAIL_FROM`, `CANARY_MAIL_TO` | `RESEND_API_KEY` and the throwaway sink the canary mails |
| `CANARY_R2_ACCOUNT_ID`, `CANARY_R2_ACCESS_KEY_ID`, `CANARY_R2_SECRET_ACCESS_KEY`, `CANARY_R2_BUCKET` | the four `STORAGE_R2_*` keys |
| `CANARY_POLAR_ACCESS_TOKEN`, `CANARY_POLAR_PRODUCT_PRO` | `POLAR_ACCESS_TOKEN`, `POLAR_PRODUCT_PRO`. `POLAR_SERVER` is pinned to `sandbox` in the workflow and is not a secret |
| `CANARY_CLERK_SECRET_KEY`, `CANARY_CLERK_FRONTEND_API_URL` | `CLERK_SECRET_KEY`, `CLERK_FRONTEND_API_URL` |
| `CANARY_CLERK_USER_SUBJECT`, `CANARY_CLERK_SESSION_JWT`, `CANARY_CLERK_ORG_SUBJECT` | optional; each widens the Clerk row from the JWKS document to the Backend API user read and the v2 organisation claim block |
| `CANARY_POSTHOG_API_KEY`, `CANARY_POSTHOG_HOST` | `POSTHOG_API_KEY`, `POSTHOG_HOST` (host defaults to the manifest's) |
| `CANARY_SENTRY_DSN` | `SENTRY_DSN` |
| `CANARY_LLM_API_KEY`, `CANARY_LLM_MODEL`, `CANARY_LLM_BASE_URL` | `LLM_API_KEY`, `LLM_MODEL`, `LLM_BASE_URL` |
| `CANARY_CACHE_REDIS_URL`, `CANARY_CACHE_REDIS_TOKEN` | `CACHE_REDIS_URL`, `CACHE_REDIS_TOKEN` |
| `CANARY_RATE_LIMIT_REDIS_URL`, `CANARY_RATE_LIMIT_REDIS_TOKEN` | `RATE_LIMIT_REDIS_URL`, `RATE_LIMIT_REDIS_TOKEN` |
| `CANARY_TYPESENSE_URL`, `CANARY_TYPESENSE_API_KEY`, `CANARY_TYPESENSE_COLLECTION` | `TYPESENSE_URL`, `TYPESENSE_API_KEY`, and the pre-created collection the probe writes into |
| `CANARY_ABLY_ENDPOINT`, `CANARY_ABLY_API_KEY` | `ABLY_ENDPOINT`, `ABLY_API_KEY` |
| `CANARY_OTLP_ENDPOINT`, `CANARY_OTLP_API_KEY` | `OTLP_ENDPOINT`, `OTLP_API_KEY` |
| `CANARY_OTLP_AUDIT_EXPORT_URL` | `OTLP_AUDIT_EXPORT_URL` |
| `CANARY_NEON_API_KEY`, `CANARY_NEON_PROJECT_ID` | `NEON_API_KEY`, `NEON_PROJECT_ID` |
| `CANARY_KNOCK_API_KEY`, `CANARY_KNOCK_WORKFLOW`, `CANARY_KNOCK_RECIPIENT` | `KNOCK_API_KEY`, plus the throwaway workflow key and recipient the canary triggers |
| `CANARY_SVIX_API_KEY`, `CANARY_SVIX_APP` | `SVIX_API_KEY`, plus the throwaway consumer application the canary messages |
| `CANARY_OPENMETER_API_KEY`, `CANARY_OPENMETER_URL`, `CANARY_OPENMETER_SUBJECT` | `OPENMETER_API_KEY`, `OPENMETER_URL`, plus the throwaway metering subject |

To run one row locally, export its keys and `GGG_LIVE_CANARY=1`; the skip line
for every unconfigured row prints the exact assignment it wants.

### What a canary cannot check

Stated rather than implied, because a probe that only proves a call returned
2xx is worth little and pretending otherwise is worse. Three rows are in that
position and say so in the table itself: `realtime-ably` has no read-back at
all (the seam's only read path needs a websocket transport the adapter
refuses), `analytics-posthog` gets nothing back from ingestion beyond a status
code, and `audit-export-otlp` exposes only an error from `Export`. Polar's
subscription-event payload arrives only from a real purchase, so its webhook
parser keeps the fake as its only coverage, and Clerk's v2 organisation claim
block is checked only when a session token is supplied.

## Fuzz

`make fuzz` runs every trust-boundary fuzz target, `FUZZTIME` (default `8s`)
each:

| Target | Package | Invariant |
|---|---|---|
| `FuzzFakeVerifier` | `internal/identity` | session-token parsing never panics; claims round-trip |
| `FuzzSanitizeFilename` | `internal/mail/dev` | a dev email filename can never contain `/`, `\`, `..` or NUL |

The gate is CI-only; the `check` gate stays fast by decision. A fuzz target no
gate invokes is an unfuzzed parser, so
`TestFuzzGateInvokesEveryFuzzTarget` in `internal/modkit` scans every
`_test.go` in the tree for `func FuzzXxx` and fails when the `fuzz` recipe does
not name it. Adding a target to the tree is therefore all it takes to add it to
the gate — the check is against the declared targets, never a count.

## Deliberately absent

- **golangci-lint** — `go vet` plus `govulncheck` cover the repo's risk
  surface, and a new tool needs a manifest entry.
- **Coverage thresholds** — the number is noise without a target, and a hard
  floor punishes you for deleting sample code.
- **Visual in `make check`** — the container run is slow and needs Docker, so
  the local gate stays fast; CI's required `visual` job is where baselines are
  compared, and `make visual` reproduces it exactly.
- **Fuzzing in `make check`** — same reason; `make fuzz` runs in CI.
