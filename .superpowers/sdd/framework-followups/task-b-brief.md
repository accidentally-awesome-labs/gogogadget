### B. Give every e2e spec a feature-module owner and enforce the no-orphan rule

Binding plan requirements (verbatim):

> Make every module that advertises `tests.e2e` own the corresponding spec, and generate the
> Playwright project/surface/persona inventory from installed manifests. A minimal/profile
> install must have no orphan tests for absent pages, while `profile/full` retains the existing
> e2e, visual, accessibility, smoke, race, fuzz, and Docker gates. (task 8)

> … fill every manifest's `tests.e2e`/capability declarations so installed-module verification
> is enforced on each change. (task 10)

Current defect: `ggg/system/e2e` owns and declares all 20 specs
(`registry/modules/system/e2e/module.json`: `files` lists every `e2e/*.spec.ts`, `tests.e2e`
lists all of them). It is the only module in the 295-module catalog with any `tests.e2e`
declaration. So installing `ggg/workflow/billing-checkout` brings no billing spec, and
installing `ggg/system/e2e` brings a billing spec whether or not billing is installed — the
exact orphan condition the plan forbids.

#### What to build

1. Move per-feature spec ownership to the module whose surface the spec exercises. Ownership is
   a manifest edit (`files` + `tests.e2e`), not a file move: core manifests use the repo path as
   both `source` and `target`. Required assignments:

   | spec | owner |
   |---|---|
   | `e2e/billing.spec.ts` | `ggg/workflow/billing-checkout` |
   | `e2e/webhooks.spec.ts` | `ggg/workflow/outbound-webhooks` |
   | `e2e/notifications.spec.ts` | `ggg/workflow/notification-inbox` |
   | `e2e/files.spec.ts` | `ggg/workflow/files` |
   | `e2e/projects.spec.ts` | `ggg/workflow/projects` |
   | `e2e/impersonation.spec.ts` | `ggg/workflow/impersonation` |
   | `e2e/auth.spec.ts` | `ggg/workflow/auth-session` |
   | `e2e/locale.spec.ts` | `ggg/system/i18n` |
   | `e2e/export.spec.ts` | the export workflow the spec actually drives — read it first |
   | `e2e/account.spec.ts` | the account module the spec actually drives — read it first |
   | `e2e/admin.spec.ts` | the admin module whose surface dominates the spec — read it first |

   Cross-cutting suites stay with `ggg/system/e2e`, because they assert shell/platform
   behaviour rather than one feature: `a11y.spec.ts`, `a11y-states.spec.ts`, `keyboard.spec.ts`,
   `loading.spec.ts`, `progressive.spec.ts`, `mobile.spec.ts`, `csp.spec.ts`, `visual.spec.ts`,
   `public.spec.ts`.

2. If a spec asserts surfaces belonging to more than one module, split it along module lines
   rather than picking an owner and leaving cross-module assertions behind. Keep test names and
   assertions intact when splitting; a split file is owned by the module it now covers.

3. Add the enforcement invariant as a Go test in `internal/modkit`: for every manifest-declared
   `tests.e2e` spec, parse the literal navigation targets in that spec (`page.goto('…')`,
   `context.newPage()` + `goto`, `request` paths — literals only, skip computed strings) and
   assert every literal app/admin/public path resolves to a route declared by the owning module
   or by one of its transitive `requires`. A spec that reaches a route no owner-reachable module
   declares is an orphan and must fail the test with the spec, path and owner named. This is the
   check that makes "no orphan tests for absent pages" mechanical instead of aspirational.

4. Keep the generated Playwright inventory (`e2e/generated/*.ts`) correct: it is generated from
   installed manifests, so it must regenerate byte-identically through
   `go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline` with the new ownership,
   and `profile/full` must still resolve every spec.

#### Acceptance

- Every `e2e/*.spec.ts` has exactly one owning manifest (no file owned twice — the engine
  refuses that anyway) and appears in that owner's `tests.e2e`.
- The new orphan-enforcement test passes, and fails when you temporarily point one spec at a
  wrong owner (verify that by mutation, then revert).
- `go test ./internal/modkit ./internal/gggcli -count=1` passes.
- `go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline && go run ./cmd/ggg sync --check --offline` clean.
- `cd e2e && npx playwright test --list` still enumerates the full suite (structure unchanged);
  if you split a spec, run the split files: `cd e2e && npx playwright test <files> --reporter=line`
  against a running stack (`docker compose up -d ggg-system-database-postgres-docker-postgres`).
- Do NOT run the whole e2e suite, `make check`, visual, or any project-wide formatter/linter.
