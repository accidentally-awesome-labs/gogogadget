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

`ggg test` runs a layer at a time — `unit`, `integration`, `e2e`, `visual`,
`smoke`, or `all` — and `ggg check` is the commit gate: generate → drift check
(`sync --check --offline`) → `go vet` → `go test` → `go build`. The `make`
targets are thin aliases over `bin/ggg`.

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

## Unit

Plain `go test`, no database. Examples: the `Entitled` status matrix, plan
limits and MRR math, config validation, the `e2e:` token parser, the JSON
error shape.

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

## Integration

`internal/db/testdb` gives **every package its own database**
(`gogogadget_test_<name>`), dropped, recreated, and migrated at `Open` —
`go test ./...` runs packages in parallel, and a shared database would let
one package's teardown nuke another's fixtures. Tests self-skip when the
server is unreachable (`ggg services up` locally; CI provides it).

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

## End-to-end

Playwright (Chromium) drives the real server on **port 18080** — never the
dev port 8080, so the suite cannot attach to a stray dev server. The
`webServer` boots `go run ./cmd/server` with `APP_ENV=test`,
`DEV_AUTH_BYPASS=true`, a placeholder `CLERK_PORTAL_URL`, and
`TEST_NOW=2026-01-15T00:00:00Z`, and blank `CLERK_PUBLISHABLE_KEY` /
`CLERK_SECRET_KEY` — the server auto-loads `.env` in development, and a real
dev key would boot clerk-js and contradict the suite's own "no third-party
request" assertions. `globalSetup` reseeds the disposable `gogogadget_e2e`
database on every run via `go run ./cmd/seed -reset -registry e2e`, which
loads the module-owned fixtures under `internal/db/testdata/seed/e2e/`.

Login is a cookie, not a hosted page:

```ts
const context = await loginAs(browser, 'pro');
// sets __session=e2e:user_pro:org_pro:org:admin
```

The personas are generated. `e2e/generated/personas.ts` is rendered from the
`personas` declarations of the installed modules and exports `PersonaId` —
`free`, `pro`, `admin`, `support`, `disabled`, `noorg`, `noactive`, `toggle`,
`deleteme` — plus `sessionFor`. `e2e/helpers.ts` imports it rather than
keeping a second literal map, so a module that adds a persona cannot leave the
helper behind. Each persona owns disjoint orgs and rows, so `fullyParallel`
specs never mutate each other's fixtures.

Every spec belongs to the module whose surface it drives, declared in that
module's `files` and `tests.e2e`. `ggg/workflow/billing-checkout` brings
`e2e/billing.spec.ts`; `ggg/workflow/admin-flags` brings
`e2e/admin-flags.spec.ts`. A profile that installs no billing therefore
installs no billing spec.

Ownership splits three ways:

- `ggg/system/e2e` is the **harness** — `playwright.config.ts`,
  `global-setup.ts`, `helpers.ts`, `package.json`, `package-lock.json` — and
  requires nothing but `ggg/system/project-base`. Every spec-owning module
  requires it, because every spec imports `@playwright/test` and `./helpers`.
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

Both go through `scripts/visual-run.sh`, which extracts the
`@playwright/test` version from `e2e/package.json`, resets `gogogadget_e2e`,
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

The gallery baselines are the highest-leverage ones: a shade, spacing or
variant regression anywhere in the component layer shows up as one named
failing screenshot instead of leaking into a page nobody captures.
`a11y.spec.ts` sweeps the same generated surfaces with axe in both themes,
`a11y-states.spec.ts` opens dialogs, menus, popovers and pickers before
scanning, and `keyboard.spec.ts` drives focus return, roving tabindex and the
no-JS fallbacks. These are dev-only routes (`DEV_AUTH_BYPASS`), which the
visual and e2e harnesses both set.

## CI

Seven jobs. `test` sets up Go and Postgres, then runs the gate: `make setup` →
`make generate` → `git diff --exit-code -- ':!gogogadget.lock.json'`
(generated code is committed and fresh, which is also what proves no registry
drift) → `go vet` → `govulncheck` → `go test -race -cover ./...` →
`make fuzz` → `go build`.
`e2e`, `visual`, `smoke`, `docker`, `registry-core` and `registry-external`
all depend on `test`: `e2e` installs Chromium and runs `make e2e`; `visual`
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
