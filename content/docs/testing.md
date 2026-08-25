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

One of those baselines is `/dev/gallery` (`gallery-light` / `gallery-dark`),
which renders every design token and every component in every variant on a
single page — so a shade, spacing or variant regression anywhere in the
component layer shows up as one failing screenshot instead of leaking into a
page nobody screenshots. `a11y.spec.ts` sweeps the same URL with axe in both
themes. It is a dev-only route (`DEV_AUTH_BYPASS`), which the visual and e2e
harnesses both set.

## CI

Two jobs. `test` sets up Go and Postgres, then runs the gate: `make setup` →
`make generate` → `git diff --exit-code` (generated code is committed and
fresh) → `go vet` → `govulncheck` → `go test -cover ./...` → `go build`.
`e2e` depends on it, installs Chromium, and runs `make e2e`, uploading the
Playwright report on failure. There is deliberately no numeric coverage
gate — a hard threshold punishes you for deleting sample code.

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

## CI shape

`test` runs `go test -race -cover ./...` plus `make fuzz`; a `smoke` job
boots the built server against the CI Postgres and drives
`scripts/smoke.sh`; a `docker` job builds the image to catch Dockerfile
drift. Deliberately absent: golangci-lint (vet + govulncheck cover the repo's
risk surface; new tools need a manifest entry), coverage thresholds (the
number is noise without a target), and visual baselines (the Linux-pinned
container flow is local `make visual-update` only).
