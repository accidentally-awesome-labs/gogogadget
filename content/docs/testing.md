---
title: Testing
description: Four test layers, the decision rule, and the fixtures that make them deterministic.
section: Guides
weight: 23
---

Four layers, one decision rule:

| Layer       | Runs on                                   | Write it for                                                        |
|-------------|-------------------------------------------|---------------------------------------------------------------------|
| Unit        | nothing (no DB)                           | pure logic — entitlements, config, plans, token hashing             |
| Integration | real Postgres (`TEST_DATABASE_URL`)       | handler/route behavior — webhooks, guards, limits, API auth         |
| End-to-end  | Playwright (real server + DB + browser)   | user flows — HTMX CRUD, redirects, toasts                           |
| Visual      | Playwright screenshots (dockerized)       | pixel-level layout, in both themes                                  |

**Pure logic → unit. Handler behavior → integration. User flow → e2e. Pixels
→ visual.** Never reach for a heavier layer than the behavior needs.

Which of those a given change needs is answerable without guessing. Every
module declares its test inventory, and `ggg info` prints it as commands you
can run verbatim:

```console
$ go run ./cmd/ggg info workflow/projects
…
  verify   go test -count=1 ./internal/web
```

Go packages become `go test -count=1 ./<pkg>`, declared e2e and accessibility
specs become `cd e2e && npx playwright test <spec> --reporter=line`, and a
declared visual surface becomes `./scripts/visual.sh` — never a plain
`playwright test`, because baselines only match inside the pinned container.

## Unit

Plain `go test`, no database. Examples: the `Entitled` status matrix, plan
limits and MRR math, config validation, the `e2e:` token parser, the JSON
error shape.

## Integration

`internal/db/testdb` gives **every package its own database**
(`gogogadget_test_<name>`), dropped, recreated, and migrated at `Open` —
`go test ./...` runs packages in parallel, and a shared database would let
one package's teardown nuke another's fixtures. Tests self-skip when the
server is unreachable (`docker compose up -d db` locally; CI provides it).

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

Five jobs. `test` sets up Go and Postgres, then runs the gate: `make setup` →
`make generate` → `git diff --exit-code` (generated code is committed and
fresh, which is also what proves no registry drift) → `go vet` →
`govulncheck` → `go test -race -cover ./...` → `make fuzz` → `go build`.
`e2e`, `visual`, `smoke` and `docker` all depend on `test`: `e2e` installs
Chromium and runs `make e2e`; `visual` runs `make visual`, which owns its own
seeding and host server — do not add seed or start-server steps beside it,
because a second process cannot bind `:18080` and the baselines only
reproduce against the harness that wrote them; `smoke` boots the built binary
against the CI Postgres and drives `scripts/smoke.sh`; `docker` builds the
image to catch Dockerfile drift. Both `e2e` and `visual` upload their
Playwright report on failure — a red visual job without the expected/actual/
diff images is unactionable. There is deliberately no numeric coverage gate:
a hard threshold punishes you for deleting sample code.

See [Database](/docs/database) for `TEST_DATABASE_URL` mechanics and
[Frontend](/docs/frontend) for the `data-testid` contract.

## Seam contract suites

Every provider seam has an in-package `contract_test.go`: an unexported
`run<Seam>Contract(t, factory)` table suite that every implementation runs —
the real client (against an `httptest` fake of the provider's wire format)
and the mocks/no-ops. A mock that drifts from the real contract fails the
same suite the real client runs. Faked today: Polar (billing), Resend (mail,
via the SDK's exported `BaseURL`), Clerk JWKS (identity, custom JWKS URL +
locally generated RSA key), Sentry (global hub + httptest DSN), PostHog
(analytics, `NewWithConfig` endpoint), R2 (storage, stateful fake endpoint),
and the OpenAI-compatible LLM client. No unfakeable impl remains; the
compile-time-assertion fallback the pattern allows was needed nowhere.

## Fuzz

Two trust-boundary parsers fuzz for 15s each via `make fuzz` (CI-only; the
`check` gate stays fast by decision): `FuzzFakeVerifier` (session-token
parsing — never panics, claims round-trip) and `FuzzSanitizeFilename` (dev
email filenames — output can never contain `/`, `\`, `..`, or NUL).

## Deliberately absent

- **golangci-lint** — `go vet` plus `govulncheck` cover the repo's risk
  surface, and a new tool needs a manifest entry.
- **Coverage thresholds** — the number is noise without a target, and a hard
  floor punishes you for deleting sample code.
- **Visual in `make check`** — the container run is slow and needs Docker, so
  the local gate stays fast; CI's required `visual` job is where baselines are
  compared, and `make visual` reproduces it exactly.
- **Fuzzing in `make check`** — same reason; `make fuzz` runs in CI.
