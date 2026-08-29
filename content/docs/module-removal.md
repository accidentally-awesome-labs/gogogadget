---
title: Module removal and data retention
description: The five removal policies, why applied migrations are immutable and forward-only, and what --purge-data actually requires.
section: Modules
weight: 32
---

Removal is the operation with the most reasons to refuse, and every one of them
fires **before a single byte moves**. There is no force flag and no
`--overwrite`: if the CLI cannot remove a module without destroying something it
cannot reconstruct, it says so and stops.

```sh
go run ./cmd/ggg remove component/carousel --dry-run
```

```text
registry f7e08a09a7bb213228759283f08b33c4cadd6495955efaf02505bdc5dd50f003
  update    intent     gogogadget.json
  update    lock       gogogadget.lock.json
  delete    authored   internal/web/templates/ui/carousel.templ
  delete    generated  internal/web/templates/ui/carousel_templ.go
  delete    authored   internal/web/templates/ui/carousel_test.go
  delete    authored   static/ui/carousel.js
```

Note the `generated` line. A module declares its sources; the build output
derived from them is owned by the emitter, and removal has to take it along. A
deleted `carousel.templ` whose `carousel_templ.go` survived would still define
the renderer, and the removal would be a report of work that did not happen.

## The five policies

Every manifest declares exactly one `removal_policy`. The value is the module
author's statement about what removing it costs.

| Policy | `ggg remove` behavior |
|---|---|
| `free` | Removes cleanly. Nothing persists that outlives the source. |
| `retain-data` | Removes the source; historical rows, schema, and migrations stay. |
| `drain-required` | Refuses unless the manifest supplies a reviewed forward **neutralization** migration. |
| `replacement-required` | Always refuses. Replacing it is a manual migration, not a removal. |
| `major-version-only` | Always refuses. Removing it is a breaking change for consumers. |

The authoritative per-module answer is the **Removal** column on the [Module
reference](/docs/module-reference) page, which is generated from the manifests.
As of this writing the modules that are not `free` are:

- `replacement-required` — `element/ui-core`, `system/apphost`, `system/config`,
  `system/database`, `system/i18n`, `system/modkit`, `system/organizations`,
  `system/security`, `system/server`, `system/static`, `workflow/appearance`,
  `workflow/auth-session`. These are the floor the rest of the graph stands on.
  A project without a config parser or a route table is not a project with one
  fewer feature; it is a different program.
- `major-version-only` — `system/api`, `system/billing`, `system/identity`,
  `system/rate-limit`, `workflow/billing-webhook`. Something outside your
  repository depends on these: a customer's API client, a payment provider's
  webhook endpoint. Removal is not yours to do quietly.
- `drain-required` — `system/jobs`. Rows in the queue outlive the code that
  handles them.
- `retain-data` — `page/blog`, `page/changelog`, `system/announcements`,
  `system/audit`, `system/content`, `system/feature-flags`,
  `system/impersonation`, `system/notifications`, `system/schedules`,
  `system/storage`, `system/usage`, `system/webhooks`,
  `workflow/api-token-lifecycle`, `workflow/notification-preferences`,
  `workflow/outbound-webhooks`. An audit trail whose feature was uninstalled is
  still an audit trail.

`replacement-required` also cannot be dropped by omission: a profile member with
that policy is selected even when it is listed in `exclude`.

## Why removal refuses

Each of these is a distinct refusal, each exits 3, and each names what to do.

**The module is not installed.** Nothing to remove.

**Something else requires it.**

```text
error: module system/jobs is required by page/admin-jobs; remove the dependent first
```

Reverse dependencies are read from the lock's `required_by`, so this is answered
without resolving anything.

**The policy forbids it.** `replacement-required` and `major-version-only` refuse
outright. `drain-required` refuses when its manifest declares no neutralization
migration — which is the state `system/jobs` is in today, so it is currently not
removable at all, and that is the correct answer rather than an oversight to work
around.

**A module-owned file is locally modified.**

```text
error: module X owns locally modified file Y; run ggg diff X and revert or back up
       the customization before removing
```

Removal deletes only pristine module-owned files. Your edits are work the CLI
cannot reproduce, so deleting them is not a decision it is willing to make on
your behalf. Revert, back up, or delete the customization deliberately, then
remove.

**A module-owned file is missing.** Also a refusal: an absent owned file means
the tree and the lock disagree about what exists, and a removal that quietly
tolerated it would hide that.

**A `drain-required` removal was attempted offline.** Materializing the
neutralization migration needs the registry at the lock's commit, and
`--offline` cannot fetch it.

`gogogadget.json`, the generated registries, and every source path are untouched
in all of these cases. Generated outputs are rebuilt only after every authored
removal has passed.

## Migrations are immutable and forward-only

This is the single strongest guarantee in the module system, and it is what makes
"remove a feature" a safe operation rather than a gamble.

- **A migration number is allocated once and kept forever.** A new logical
  migration takes the next free global number at install time, and that mapping
  is recorded in the lock permanently. `ggg update` never renumbers or rewrites
  one; a payload that changed after allocation is a hard error
  (`migration … payload changed after immutable allocation`).
- **The deployed ledger is adopted, not rebuilt.** This repository's migrations
  `0001`–`0019` keep their exact filenames and checksums. Manifests claim their
  tables logically; nothing is split, renamed, or renumbered.
- **Removal retains migration files, schema, and data by default.** Deleting a
  module's source does not delete its history.
- **There is no down migration.** Not "we discourage them" — the CLI has no
  concept of one. Reversal is expressed as a *new forward* migration that a human
  reviewed.

And the boundary that makes the guarantee checkable: **`ggg` never touches a
database.** Nothing under `internal/modkit` imports `database/sql`, `pgx`, or
`goose`. It materializes migration *files*; applying them is the server's job at
boot, exactly as for any other migration. A CLI that could connect to your
production database to "clean up" would be a CLI whose refusals you could not
trust.

## `--purge-data`

`--purge-data` is only valid on `ggg remove`, and it does one specific thing: it
appends the module's declared reviewed forward **teardown** migration after the
reverse-dependency checks pass. It does not issue a `DROP`, and it does not
inspect a single row.

Its requirements are exact:

1. The module must be `drain-required`. On anything else you get a warning
   diagnostic and no effect:

   ```json
   { "code": "purge_not_applicable", "severity": "warn",
     "module": "component/carousel",
     "message": "--purge-data has no effect: module is not drain-required" }
   ```

2. The manifest must declare a migration of kind `purge`. Without it:

   ```text
   error: --purge-data requires module X to declare a reviewed forward teardown migration
   ```

The asymmetry is deliberate. A `drain-required` module needs a **neutralization**
migration to be removable at all — one that disables schedules and terminally
marks or cancels persisted work *before the new binary starts*, so the queue does
not contain rows no handler exists for. Purging the data afterwards is a second,
separate, opt-in decision. Bundling them would make "uninstall this feature"
silently mean "delete this history".

## What happens to persisted work

Job rows outlive job handlers, so the queue has an explicit answer rather than a
hope. A persisted job kind with no installed handler **dead-letters immediately**
with the literal error code `module_uninstalled` — not eight retries against code
that no longer exists. Source removal performs no row query at all; the
neutralization migration is what makes the queue consistent, and schedule and job
removal generation refuses to proceed without it.

## Where to go next

- [Module anatomy and lifecycle](/docs/modules) — what `data` and
  `removal_policy` declare, and the rest of the lifecycle.
- [CLI and registry](/docs/cli) — exit codes and the JSON envelope.
- [Module reference](/docs/module-reference) — the generated Removal column.
- [Database](/docs/database) — how migrations are applied at boot.
- [Background jobs](/docs/background-jobs) — the queue whose rows outlive code.
