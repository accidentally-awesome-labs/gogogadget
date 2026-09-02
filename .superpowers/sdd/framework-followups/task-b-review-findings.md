# Task B review findings — fix round 1

Source: ReviewE2eOwnership of afc4dcd..7b922a3. Spec compliance WARN, task quality FAIL.
Strengths confirmed by the reviewer: the splits are byte-faithful, the 668→669 accounting closes
on the one deliberate new title, the `locale.spec.ts` deviation is the *better* assignment, and
ownership is exclusive and complete. Fix the items below.

## Critical

- **C1 — 25 of 34 specs are owned by modules that cannot install the harness they import.**
  `ggg/system/e2e` owns `e2e/playwright.config.ts`, `e2e/package.json`, `e2e/package-lock.json`,
  `e2e/global-setup.ts`, `e2e/helpers.ts` (`registry/modules/system/e2e/module.json:121-181`),
  and **nothing requires `ggg/system/e2e`** (grep of `registry/` returns only its own id and the
  two profiles). Every reassigned spec imports `@playwright/test` and `./helpers`, so installing
  e.g. `ggg/workflow/billing-checkout` writes a spec into a tree with no config, no helpers and
  no package.json. That breaks the promise your own test comment states
  (`internal/modkit/e2e_ownership_test.go:16-17`) and it held trivially *before* this change.
  Fix: split the module. A leaf harness module owns config/package/package-lock/global-setup/
  helpers and requires nothing beyond `ggg/system/project-base`; a second module owns the nine
  cross-cutting sweeps plus `visual.spec.ts-snapshots/` and carries the ten page `requires` this
  commit added. Then every spec-owning module requires the harness id. The harness half is a
  leaf, so no cycle is possible.

- **C2 — Make harness reachability permanent.** Extend
  `TestEveryE2ESpecOnDiskHasExactlyOneOwner`: for every declared spec, the owner must reach the
  module owning `e2e/playwright.config.ts`. The `reach` map already exists in `specOwnership`.

## Important

- **I1 — Four specs assert surfaces their owner cannot reach.** Add the missing `requires`:
  `admin-user-governance` → `page/admin-overview`, `page/admin-flags`, `page/admin-audit`
  (`admin-user-governance.spec.ts:56-59` loops literal `/admin`, `/admin/users`, `/admin/flags`,
  `/admin/audit`); `notification-inbox` → `page/projects` (`notifications.spec.ts:80-82`);
  `account-delete` → `page/home` (`account-delete.spec.ts:35` asserts `toHaveURL(/\/$/)`).
  For `auth-session` → `/app/projects` and `/app/settings/account` (`auth.spec.ts:81-84,118-120`):
  `page/settings-account` requires `auth-session` back, so either move the Settings-link leg into
  a spec owned by a module that reaches that page, or document the exception explicitly. Do not
  leave it undeclared and unmentioned.
- **I2 — The resolver is method-blind.** `surfaceRouteTable` keys `byPattern` on pattern only
  (`e2e_ownership_test.go:173-187`) and `resolve` always probes GET (`:205`), so
  `ggg/workflow/admin-flags` is credited with serving `/admin/flags` because it declares
  `POST /admin/flags` — deleting its `requires ggg/page/admin-flags` would leave the gate green.
  Same self-match shields `admin-announcements`, `admin-schedules`, `admin-content`. Key on
  method+pattern (or register only GET-declared patterns and resolve `request.post` against the
  POST set) so I1's new edges are actually defended.
- **I3 — Catch the loop-variable case.** Extend the literal extractor to collect string literals
  from array literals (`for (const p of ['/admin', …])`). That alone would have caught the
  sharpest instance in I1.
- **I4 — In-place ownership transfer breaks `ggg sync` for any already-synced derivative.**
  `ownership` is built once from the previous lock (`internal/modkit/reconcile.go:354`) and never
  updated as modules are processed, so `classifyAuthoredTarget` refuses
  `target %s is owned by %s, not %s` (`internal/modkit/target_plan.go:57-59`) even when the old
  owner emits a `ChangeDelete` in the same pass. Eleven files move owner in this commit — the
  first core-catalog change that requires it — and a consumer's next `ggg sync` refuses with no
  migration path. Fix the engine: when the same plan removes the target from its previous owner
  and installs it under the new one, the transfer must succeed in one pass, while a genuine
  two-owner collision must still refuse, and a locally modified file must still refuse rather
  than being silently overwritten. Add a modkit test for all three cases.

## Minor

- **M1** — `content/docs/testing.md:117-119` claims computed targets "come from the generated
  inventory and are skipped". False for `admin-user-governance.spec.ts:56-57`, a hand-written
  literal loop. State the real limitations: click navigation, loop variables, `toHaveURL`/
  `waitForURL` destinations.
- **M2** — `billing.spec.ts:31-33` asserts `/app/billing/confirm`, declared by the
  environment-selected adapter `ggg/system/billing-local`
  (`registry/modules/system/billing-local/module.json:213-215`), not by the owner or anything it
  requires. Record it; extending the gate to click navigation needs a deliberate adapter-aware
  answer, so do not paper over it.
- **M3** — restore the blank line between the two remaining tests in `notifications.spec.ts`.
