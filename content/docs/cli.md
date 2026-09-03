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

The command lives at `cmd/ggg`; presentation and dispatch are in
`internal/gggcli`, and resolution, planning and apply stay in
`internal/modkit`. `ggg setup` builds `bin/ggg` from the project's own source,
and every later command rides that binary:

```sh
bin/ggg catalog
bin/ggg info ggg/component/badge
```

`ggg generate` refreshes mutable directory registries, runs
`ggg sync --offline`, then templ, sqlc and Tailwind; `ggg check` runs
`generate`, then `ggg sync --check --offline`, vet, tests and build — so the
local gate itself proves there is no generated drift. The `make` targets are
thin aliases over `bin/ggg`.

## The two files

| File | Owner | Purpose |
|---|---|---|
| `gogogadget.json` | you | Declared intent: registries, modules, exclusions, provider selections, deployment module |
| `gogogadget.lock.json` | `ggg` | Resolved truth: provenance ledgers, dependency order, per-environment runtime orders, and a per-file digest ledger |

Both are committed. This repository's own intent file is the whole catalog
with a few deliberate omissions:

```json
{
  "schema": 2,
  "registries": [{ "namespace": "ggg", "source": "directory", "path": "registry" }],
  "modules": ["ggg/profile/full"],
  "exclude": ["ggg/component/table-empty", "ggg/element/divider", "ggg/system/deploy-docker"],
  "providers": { "…": {} },
  "deployment": "ggg/system/deploy-fly"
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
| `ggg version`, `ggg help [COMMAND]`, `ggg completion bash\|zsh\|fish` | no | Version, derived help, derived completions |
| `ggg catalog` | no | Lists catalog modules with installed state |
| `ggg info MODULE` | no | Prints one module's full contract |
| `ggg diff` | no | Lists owned files that differ from their installed base |
| `ggg doctor [--runtime]` | no | Lock, conflict and candidate health; `--runtime` adds provider keys, provider health, deployment linkage and backup policy |
| `ggg sync --check` | no | Fails when the tree drifts from the lock |
| `ggg provider list\|test`, `ggg deploy plan\|status\|logs` | no | Observation only |
| `ggg registry validate [--closures core\|external\|all]` | no | Loads the catalog, then installs, compiles, tests and removes every closure of that family in a throwaway derivative (default `all`) |
| `ggg new DIR` | **yes** | Creates a project: profile, provider selections, deployment, registry |
| `ggg init` | **yes** | Initializes or adopts the current directory |
| `ggg add MODULE...` / `ggg remove MODULE...` | **yes** | Selects or deselects modules, then reconciles |
| `ggg update [MODULES...]` | **yes** | Advances named modules, or one registry's ref |
| `ggg sync` | **yes** | Reconciles the tree to the declared intent |
| `ggg resolve MODULE` | **yes** | Records a decision for one conflicted file |
| `ggg create KIND NAME` | **yes** | Writes a module into the project's mutable registry |
| `ggg provider set\|configure\|provision` | **yes** | Changes a slot's selection, its declared inputs, or provisions its target |
| `ggg deployment set MODULE`, `ggg deploy apply\|rollback\|secrets` | **yes** | Deployment selection and remote operations |
| `ggg db migrate\|status\|seed\|reset\|backup\|restore\|restore-drill` | **yes** | Database lifecycle |
| `ggg setup`, `generate`, `services`, `dev`, `check`, `test`, `build` | **yes** | Trusted tasks with fixed argv |
| `ggg registry build\|init\|keygen\|sign\|verify\|rotate\|add\|remove\|update` | **yes** | Registry authoring and source management |
| `ggg migrate schema-1`, `ggg cache prune`, `ggg identity link` | **yes** | One-way schema upgrade, cache pruning, audited identity mapping |
| `ggg ui` | **yes** | The interactive console (contributed by `ggg/system/cli-ui`) |

`ggg registry build` mutates the **registry**, not the project: it rewrites
manifest digests and `registry/*.json` indexes. In a self-hosting registry the
payload and its manifest live in the same tree, so editing a module's own source
stales its manifest and every later `sync` refuses on a digest mismatch. Running
`registry build` is the authoring step that closes that loop. It only ever
touches manifests, never payloads.

### Flags

`ggg help`, `ggg help COMMAND` and the shell completions are all **derived
from the same command table the dispatcher reads** (`CommandTable()` in
`internal/gggcli/table.go`), so a command or flag cannot exist in one place
and be invisible in another. That table is the authoritative list; run
`ggg help COMMAND` rather than trusting a copy.

Notable behavior, all enforced rather than advisory:

- Flags may appear before, after, or between positional arguments.
- `--json` implies noninteractive: it never prompts, and a command that would
  need a confirmation refuses instead.
- `ggg` with no arguments opens the console on a terminal; off a terminal it
  is the declared `interactive_terminal_required` usage failure (exit 2).
- `--accessible`, or `GGG_ACCESSIBLE=1`, switches guided forms to linear
  prompts. It is global and may appear before the command.
- `--claim` is repeatable and only meaningful where the lock is being written.
  `--claim` with `--check` is a usage error, because `--check` must not mutate.
- `--kind` accepts only `element`, `component`, `page`, `workflow`, `system`.
- `--offline` resolves only from the local or cached registry and never
  reaches the network.
- Every declared secret is redacted from prompts, plans, diagnostics, JSON and
  logs before anything leaves the process.

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
In this repository that makes both commands report tombstones alongside the 288 modules that are
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
resolved graph rather than from the lock, so its rows are the live count by
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

Three commands carry command-specific data **in addition** to those ten keys,
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

Exit 5 means the transaction rolled back. Every journalled path is restored to
exactly the bytes *and the mode* it had, directories the run created are
removed, and unrelated work is never touched.

A restore can itself fail — a full disk is as likely to defeat the write-back as
it was to cause the failure that triggered it. When it does, the error names
every path that could not be restored and the JSON envelope reports
`rolled_back: false`. That is the one case where the tree needs looking at by
hand; a plain exit 5 does not.

## Where to go next

- [Module anatomy and lifecycle](/docs/modules) — what a manifest declares and
  how install, modify, diff, sync, conflict, and remove fit together.
- [Module reference](/docs/module-reference) — every installed module, generated.
- [Module removal and data retention](/docs/module-removal) — why removal can
  refuse, and what applied migrations guarantee.
- [Configuration reference](/docs/configuration-reference) — the environment
  table, generated from the same manifests.
