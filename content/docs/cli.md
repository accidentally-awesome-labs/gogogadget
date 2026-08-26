---
title: CLI and registry
description: ggg — the module registry CLI. Every command, its real flags, the JSON envelope, and the exit codes automation branches on.
section: Modules
weight: 30
---

`ggg` is the source-registry CLI. It installs, inspects, updates, and removes
**modules** — units of GoGoGadget source that land in your tree as ordinary
files you own and can edit. It is not a package manager: nothing is fetched at
runtime, nothing is vendored into a black box, and there is no `node_modules`
equivalent. What arrives is source.

The command lives at `cmd/ggg`; all behavior is in `internal/modkit`. Run it
from the project root:

```sh
go run ./cmd/ggg catalog
go run ./cmd/ggg info component/badge
```

Repository automation always uses `go run ./cmd/ggg`, so a fresh clone never
depends on `PATH` or a prebuilt binary. `make generate` is
`go run ./cmd/ggg sync --offline` followed by templ, sqlc, and Tailwind;
`make check` runs `generate`, then `go run ./cmd/ggg sync --check --offline`,
vet, tests, and builds — so the local gate itself proves there is no generated
drift.

## The two files

| File | Owner | Purpose |
|---|---|---|
| `gogogadget.json` | you | Declared intent: which registry, which ref, which modules, which exclusions |
| `gogogadget.lock.json` | `ggg` | Resolved truth: one registry commit, dependency order, and a per-file digest ledger |

Both are committed. This repository's own intent file is the whole catalog with
two deliberate omissions:

```json
{
  "schema": 1,
  "registry": { "repository": "gogogadget/gogogadget", "ref": "main" },
  "modules": ["profile/full"],
  "exclude": ["component/table-empty", "element/divider"]
}
```

`add`, `remove`, and `update` do not install anything themselves. They edit
`gogogadget.json` and then call the same reconciler `sync` calls, which is why
there is exactly one code path from intent to bytes on disk.

## Commands

Read-only commands never write to the tree. Mutating commands write through a
transaction journal that restores the exact pre-run bytes on any failure.

| Command | Mutates | What it does |
|---|---|---|
| `ggg version` | no | Prints `ggg <version>` |
| `ggg catalog` | no | Lists catalog modules with installed state |
| `ggg info KIND/NAME` | no | Prints one module's full contract |
| `ggg diff` | no | Lists owned files that differ from their installed base |
| `ggg doctor` | no | Reports lock, conflict, and candidate health |
| `ggg sync --check` | no | Fails when the tree drifts from the lock |
| `ggg registry validate` | no | Loads the catalog and reports every id |
| `ggg init` | **yes** | Writes `gogogadget.json`; `--adopt` also writes the lock |
| `ggg add KIND/NAME...` | **yes** | Selects modules, then reconciles |
| `ggg remove KIND/NAME...` | **yes** | Deselects modules, then reconciles |
| `ggg update` | **yes** | Advances the installed graph toward one commit |
| `ggg sync` | **yes** | Reconciles the tree to the declared intent |
| `ggg resolve KIND/NAME` | **yes** | Records a decision for one conflicted file |
| `ggg registry build` | **yes** | Refreshes manifest digests and rebuilds the kind indexes |

`ggg registry build` mutates the **registry**, not the project: it rewrites
manifest digests and `registry/*.json` indexes. In a self-hosting registry the
payload and its manifest live in the same tree, so editing a module's own source
stales its manifest and every later `sync` refuses on a digest mismatch. Running
`registry build` is the authoring step that closes that loop. It only ever
touches manifests, never payloads.

### Flags

There is deliberately no `--help` text: the flag sets discard their own usage
output so a usage failure is one line naming the correct form, not a wall. The
authoritative list is here and in `internal/modkit/cli.go`.

```text
ggg init [--ref REF] [--repository REPO] [--adopt] [--claim PATH]... [--offline] [--json]
ggg catalog [--installed] [--kind KIND] [--latest] [--json]
ggg info KIND/NAME [--json]
ggg add KIND/NAME... [--dry-run] [--json]
ggg remove KIND/NAME... [--dry-run] [--purge-data] [--json]
ggg update [--ref REF] [--dry-run] [--json]
ggg diff [KIND/NAME...] [--upstream] [--json]
ggg resolve KIND/NAME --path PATH (--accept-upstream|--keep-local|--merged) [--json]
ggg doctor [--json]
ggg sync [--check] [--offline] [--claim PATH]... [--json]
ggg registry build|validate [--json]
ggg version
```

Notable behavior, all enforced rather than advisory:

- Flags may appear before, after, or between positional arguments.
  Go's `flag` package stops at the first non-flag token, which would silently
  drop the `--json` in `ggg info component/card --json`; `ggg` re-parses instead.
- `--purge-data` is rejected on anything but `remove`, and `--ref` on anything
  but `update`. A flag that is accepted and ignored is worse than one refused.
- `--claim` is repeatable and only meaningful where the lock is being written.
  `--claim` with `--check` is a usage error, because `--check` must not mutate.
- `--kind` accepts only `element`, `component`, `page`, `workflow`, `system`.
- `--offline` resolves from the local or cached registry and never reaches the
  network.

### Module states

`ggg catalog` reports one state per module:

| State | Meaning |
|---|---|
| `available` | In the catalog, not in the lock |
| `clean` | Installed, no pending update |
| `conflicted` | Installed with a staged upstream candidate awaiting `ggg resolve` |
| `removed` | A removal tombstone: deselected, with immutable history retained |

**Do not count rows to count modules.** A module removed by exclusion stays in
the lock as a `removed` tombstone, and `installedStates` maps every lock entry —
so a tombstone is reported by `ggg catalog` *and* by `ggg catalog --installed`,
because the flag filters on "present in the lock", which a tombstone satisfies.
In this repository that makes both commands print 242 rows while 240 modules are
live.

The row set and the module set are different questions, so **always filter on
`state`, and name the states you mean** rather than subtracting the one you
happen to know about today — a predicate written as `!= "removed"` is correct
now and silently wrong the first time a state is added:

```sh
# every module that is installed and usable
go run ./cmd/ggg catalog --json |
  jq '[.modules[] | select(.state == "clean" or .state == "conflicted")] | length'
```

The [Module reference](/docs/module-reference) page is generated from the
resolved graph rather than from the lock, so its 240 rows are the live count by
construction — a module absent from it is not installed.

`ggg diff` reports per-file states instead — `clean`, `modified`, `missing`, or
`unreadable` — by comparing each file's bytes against the `base_sha256` the lock
recorded. Without `--upstream` it prints only files that are not `clean`, so
silence means the tree matches the lock.

## The JSON envelope

`--json` is the noninteractive contract. Its key set is fixed: absent data
encodes as an empty collection rather than a missing key, so a consumer can
index every field without probing.

```json
{
  "ok": true,
  "command": "sync",
  "run_id": "…",
  "registry_commit": "…",
  "resolved": [],
  "changes": [],
  "generated": [],
  "conflicts": [],
  "diagnostics": [],
  "exit": 0
}
```

`run_id` is derived from the envelope's own content, so the same plan reports the
same id and a changed plan cannot reuse one.

Three commands carry command-specific data **in addition** to those eleven keys,
never instead of them:

| Command | Extra keys |
|---|---|
| `ggg catalog --json` | `modules` |
| `ggg info --json` | `module`, `state`, `links`, `verify` |
| `ggg diff --json` | `files` |

`links` and `verify` are derived, not declared. `links` reports where a module
can be *looked at* — its gallery, scenario, and route URLs — because an agent
told only which files a component owns still has to guess whether the thing is
visible anywhere, and storing those URLs in manifests would let them drift from
the routes that serve them. `verify` prints the exact commands that check the
module, so a reader does not have to know that Go tests run by package, that
Playwright lives in a sibling directory, and that visual baselines are
regenerated in a pinned container. Both are `null` or absent when the module
declares nothing to look at or nothing to run.

Human output renders the same `Plan` fields as the envelope. It is a second view,
never a second interpretation.

## Exit codes

Automation branches on these, so they are a public contract.

| Code | Name | Cause |
|---|---|---|
| `0` | ok | Success |
| `1` | runtime | Fetch, I/O, or generation error |
| `2` | usage | Bad flags, bad module id, malformed project or lock |
| `3` | refusal | Preflight said no *before writing anything* |
| `4` | conflict | Safe work completed, but drift or staged conflicts remain |
| `5` | rollback | Application failed and the tree was restored |

Exit 3 is the one worth internalizing: a refusal means nothing was written. The
planner is pure, so every namespace collision, missing dependency, cycle, digest
mismatch, locally modified file blocking a removal, and stricter removal policy
is caught before the first byte moves.

Exit 4 has two distinct sources and both are honest reports rather than errors:

- `ggg sync --check` found pending changes or generated drift. Nothing was
  written; run `ggg sync`.
- `ggg update` updated everything it safely could, and one or more modules are
  now `conflicted` with an upstream candidate staged. Local bytes are untouched;
  run `ggg diff --upstream` and then `ggg resolve`.

Exit 5 means the transaction rolled back. Pre-existing dirty generated files are
restored to exactly the bytes they had, and unrelated work is never touched.

## Where to go next

- [Module anatomy and lifecycle](/docs/modules) — what a manifest declares and
  how install, modify, diff, sync, conflict, and remove fit together.
- [Module reference](/docs/module-reference) — every installed module, generated.
- [Module removal and data retention](/docs/module-removal) — why removal can
  refuse, and what applied migrations guarantee.
- [Configuration reference](/docs/configuration-reference) — the environment
  table, generated from the same manifests.
