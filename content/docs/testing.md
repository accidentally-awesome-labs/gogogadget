---
title: Testing
description: Four test layers, the decision rule, and the fixtures that make them deterministic.
section: Guides
weight: 15
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
`TEST_NOW=2026-01-15T00:00:00Z`. `globalSetup` reseeds the disposable
`gogogadget_e2e` database on every run via
`go run ./cmd/seed -reset internal/db/testdata/seed_e2e.sql`.

Login is a cookie, not a hosted page:

```ts
const context = await loginAs(browser, 'pro');
// sets __session=e2e:user_pro:org_pro:org:admin
```

The `loginAs` users (`free`, `pro`, `admin`, `disabled`, `noorg`, `noactive`)
own disjoint orgs and rows, so `fullyParallel` specs never mutate each
other's fixtures.

Assertion discipline, by convention:

- retrying assertions only — `await expect(locator).toBeVisible()`, never a
  bare `isVisible()`;
- select by `[data-testid]` only, never by visible copy;
- shell scripts run `set -euo pipefail`, so a failed step inside a pipe fails
  the run.

Run the suite with `make e2e` (interactive mode: `make e2e-ui`).

## Visual

Screenshot specs cover the key pages in light AND dark. Baselines are
font-rendering-sensitive, so they are generated ONLY inside the pinned
Playwright Linux container:

```sh
make visual-update   # the only supported way to regenerate baselines
```

The script extracts the `@playwright/test` version from `e2e/package.json`,
runs that exact `mcr.microsoft.com/playwright:v<version>-jammy` image, and
updates `e2e/visual.spec.ts-snapshots/`. macOS screenshots diff by design —
never commit locally generated baselines. Determinism comes from `TEST_NOW`:
under `APP_ENV=test` the render clock freezes (`Config.Now()`), so every
rendered date and relative time is stable, and the e2e seed uses fixed
`2026-01-15` timestamps to match.

## CI

Two jobs. `test` sets up Go and Postgres, then runs the gate: `make setup` →
`make generate` → `git diff --exit-code` (generated code is committed and
fresh) → `go vet` → `govulncheck` → `go test -cover ./...` → `go build`.
`e2e` depends on it, installs Chromium, and runs `make e2e`, uploading the
Playwright report on failure. There is deliberately no numeric coverage
gate — a hard threshold punishes you for deleting sample code.

See [Database](/docs/database) for `TEST_DATABASE_URL` mechanics and
[Frontend](/docs/frontend) for the `data-testid` contract.
