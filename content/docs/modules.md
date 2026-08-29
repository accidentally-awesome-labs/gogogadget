---
title: Module anatomy and lifecycle
description: What a module manifest declares, how requires closure resolution works, and the install -> modify -> diff -> sync -> conflict -> remove loop.
section: Modules
weight: 31
---

A module is a unit of GoGoGadget source with one owner, a declared contract, and
a stated cost of removal. Everything in this repository is one — 240 of them,
across five kinds:

| Kind | What it is | Example |
|---|---|---|
| `element` | A leaf renderer with no dependencies beyond the UI core | `element/button` |
| `component` | A composed renderer, usually built on elements | `component/data-table` |
| `page` | One route, its template, its strings, its tests | `page/projects` |
| `workflow` | A vertical slice: mutation handlers, jobs, effects, contracts | `workflow/billing-checkout` |
| `system` | A provider with a lifecycle the runtime boots and closes | `system/jobs` |

The full inventory is on the [Module reference](/docs/module-reference) page,
generated from the manifests themselves.

## The manifest

One file per module: `registry/modules/<kind>/<name>/module.json`, validated
against `registry/schema/module.schema.json`. The document is
`{ "schema": 1, "module": { … } }` and it is **data only**. There is no `run`,
`hook`, `postinstall`, command array, plugin path, or inline code field
anywhere in the schema — installing a module cannot execute anything.

Here is the whole of `component/badge`, which is about as small as a real module
gets:

```json
{
  "schema": 1,
  "module": {
    "id": "component/badge",
    "kind": "component",
    "name": "badge",
    "revision": 1,
    "contract": 1,
    "title": "Badge",
    "description": "The Badge renderer and its options.",
    "requires": ["element/ui-core"],
    "files": [
      {
        "source": "internal/web/templates/ui/badge.templ",
        "target": "internal/web/templates/ui/badge.templ",
        "class": "templ",
        "sha256": "7a68156d…f548bf",
        "rewrite_module": true,
        "contract": true
      }
    ],
    "claims": {},
    "runtime": {
      "ui": [
        {
          "name": "badge",
          "family": "feedback",
          "signature": "templ Badge(o BadgeOpts)",
          "summary": "A small state pill: Kind picks the semantic colour.",
          "states": ["default", "error", "success"]
        }
      ]
    },
    "migrations": [],
    "environment": [],
    "docs": [],
    "tests": {},
    "data": [],
    "removal_policy": "free"
  }
}
```

Seventeen fields are **required** by the schema, and the empty ones above are
not noise. `"data": []` is a claim that this module persists nothing; `"claims":
{}` is a claim that it reserves no global name. An omitted field would be an
unknown; an empty one is a stated absence, and the removal planner and the
namespace preflight both read it as such.

### Identity and versioning

| Field | Meaning |
|---|---|
| `id` | `kind/name`, stable forever; the only way anything refers to this module |
| `kind` | One of the five above; must agree with the `id` prefix |
| `name` | The kebab-case second half of the id |
| `revision` | Integer, bumped on any change to the module |
| `contract` | Integer, bumped **only** when the public interface changes |
| `title`, `description` | Prose for the catalog and this documentation |

`revision` and `contract` are separate on purpose. A module whose behavior file
changed gets a new revision and can update independently. A module whose typed
interface changed gets a new contract, and that has consequences during
[update](#update-and-conflicts): a contract change pins the module's reverse
dependents rather than updating them into a compile error.

### Files

```json
{ "source": "…", "target": "…", "class": "templ",
  "sha256": "…", "rewrite_module": true, "contract": true }
```

`source` is the path inside the registry; `target` is where it lands in your
project. Every payload is verified against `sha256` before anything is written,
so a truncated download or a tampered archive is a refusal, not a broken tree.

`rewrite_module` asks the installer to rewrite the canonical Go module prefix to
the target project's own — read from its `go.mod`, never duplicated in
configuration. Only parsed Go import specs and templ import blocks are rewritten;
this is not a text substitution over the file.

`contract: true` marks a file as defining the module's public interface. Only
contract files pin wiring; a behavior file stays locally editable without
freezing anything downstream.

Ownership is **exclusive**. Exactly one module owns each authored file, and
identical bytes never imply shared ownership — shared behavior moves into a
shared module that consumers depend on. `TestEveryTrackedSourceFileHasAnOwner`
enforces it: an unowned source file is invisible to install, update, and
removal, so it is a bug rather than a shortcut.

### Claims

`claims` reserves global names so two modules cannot silently collide. The
namespaces are `packages`, `routes`, `jobs`, `environment`, `i18n`, `queries`,
`openapi`, `content_types`, `ui`, `assets`, and `data`. A collision is caught in
the planner, which means it is a refusal with **zero writes** rather than a
runtime surprise.

### Runtime contributions

`runtime` is how a module joins the running application without any module
importing a central registry. It is typed data — validated Go package, type, and
constructor identifiers — and the generator turns it into direct calls, so an
incorrect declaration is a compiler error on a named generated line rather than
a nil dereference at boot.

The declarable groups are `routes`, `jobs`, `janitors`, `navigation`, `slots`,
`ui`, `assets`, `queries`, `content_types`, `scenarios`, `visual`, and `system`.
See [Configuration](/docs/configuration) for `environment`, [Background
jobs](/docs/background-jobs) for `jobs`, and [Component
usage](/docs/components) for `ui`.

### Data and removal

Every module that persists state declares it:

```json
{ "table": "…", "scope": "user|org|platform",
  "export": …, "account_delete": "cascade|manual|retain",
  "organization_delete": "…" }
```

Those five fields are required, and account/org export collectors and deletion
ordering are **generated** from them — so a newly installed data module cannot
be silently omitted from a GDPR export or a delete. `removal_policy` is one of
five values covered in [Module removal and data
retention](/docs/module-removal).

### Optional fields

`locales`, `openapi`, `personas`, `vendors`, and `test_only` are optional
because most modules have nothing to say about them. `vendors` records the
source URL, version, byte count, SHA-256, and license of every third-party file
a module commits — declared per module, so removing the module removes its
vendored bytes with it. `test_only: true` is legal only under
`registry/testdata` and is never selectable from `gogogadget.json`.

## Requires closure resolution

`gogogadget.json` states intent; the resolver turns it into a graph. The
algorithm, in order:

1. **Expand selection.** Each entry in `modules` is either a module id
   (`reason: "explicit"`) or a profile id, in which case every member is
   selected (`reason: "profile"`). An unknown id is a usage error.
2. **Apply exclusions.** A profile member listed in `exclude` is skipped —
   **unless** its removal policy is `replacement-required`, which cannot be
   dropped by omission.
3. **Expand dependencies.** Every selected module's `requires` are added
   transitively (`reason: "dependency"`). This step does **not** consult
   `exclude`: an exclusion can never override a hard dependency edge. Excluding
   something another selected module requires simply gets it back.
4. **Detect cycles.** A cycle is named and refused, both during expansion and
   again in the topological sort.
5. **Order deterministically.** Dependencies precede dependents, with a lexical
   tie-break, so the resolved order is identical on every machine.

This repository selects `profile/full` and excludes `component/table-empty` and
`element/divider`, which is why the module reference lists 240 modules while the
lock file carries 242 records — the two extra are removal tombstones.

## The lock

`gogogadget.lock.json` is generated and committed. Top level:

| Field | Why it is there |
|---|---|
| `schema` | Format version |
| `registry_commit` | The single catalog commit the last resolve pinned |
| `order` | The deterministic dependency order the generators emit in |
| `modules` | One record per module, sorted |

Each module record:

```json
{
  "id": "component/badge",
  "revision": 1,
  "contract": 1,
  "source_commit": "a9847a3b…",
  "reason": "profile",
  "required_by": ["component/kanban", "component/member-item"],
  "manifest": { … },
  "files": [
    { "path": "internal/web/templates/ui/badge.templ",
      "source": "internal/web/templates/ui/badge.templ",
      "base_sha256": "7a68156d…", "local_sha256": "7a68156d…",
      "state": "clean" }
  ],
  "migrations": []
}
```

Four of those fields carry their weight in a way worth spelling out:

- **The embedded `manifest`.** The lock carries the full canonical manifest
  snapshot, not a reference to one. That is what makes `ggg sync --offline` work
  in a fresh clone with no cache and no network: the intent, the contract, and
  the digests are all in the repository.
- **Per-module `source_commit`.** Not one global commit. Independent modules can
  advance while a conflicted module and its compatibility closure stay pinned at
  their old commit, so one local edit does not freeze the whole catalog.
- **`base_sha256` versus `local_sha256`.** `base` is what upstream shipped;
  `local` is what your tree has. `state` is the derived answer — `clean`,
  `modified`, `missing`, or `conflicted`. This pair is the entire reason update
  can promise never to overwrite your edits: it can tell your bytes from
  upstream's.
- **`reason` and `required_by`.** Why the module is installed, and who would
  break if it left. `ggg remove` answers "is anything depending on this?" from
  the lock's own embedded manifests, with no resolve and no network.

Generated outputs are tool-owned, never module-owned: `*_templ.go`,
`internal/db/sqlc/*`, `static/app.css`, `static/ui-components.js`,
`static/ui-engines.js`, every `*_registry_gen.*` aggregate, and a short explicit
list of rendered non-Go outputs.

A module may still **claim** one, by declaring it with `"class": "generated"` —
`system/static` does exactly that for the three files above, which is how
removing that module takes its built CSS and aggregated scripts with it. What a
generated declaration cannot do is carry a payload. The registry neither
distributes nor verifies those bytes: the loader skips a `class: generated` entry
before it reaches the digest check, so whatever `sha256` the manifest happens to
record for one is never read by anything. The snapshot excludes generated
outputs on purpose — including one would let writing the file invalidate the
lock. They are produced by `make generate` and checked by `ggg sync --check`
re-rendering them, which is the only check that means anything for them.
Declaring a generated path under any *other* class is refused —
"generated outputs are tool-owned and cannot be authored".

In the lock those entries carry `"state": "generated"` with **empty**
`base_sha256` and `local_sha256`. That has one consequence worth knowing before
it wastes your afternoon: `ggg diff` compares each file's digest against its
recorded base, and an empty base never matches, so **every generated file shows
as `modified` in `ggg diff` permanently**. It is not drift. The two commands
answer different questions:

| Question | Command | How it answers |
|---|---|---|
| Did I change source I own? | `ggg diff` | Compares on-disk bytes against the recorded upstream base |
| Is generated output stale? | `ggg sync --check --offline` | Re-renders every aggregate and compares bytes |

A locally rebuilt generated output is `modified` against its base by
definition, which is why `ggg diff` is the wrong probe for it. The base digest
plays no part in detecting generated drift: `generatedDriftDiagnostics` renders
the aggregates and compares them byte-for-byte with the tree, emitting
`generated_missing`, `generated_drift`, or `generated_stale` for an aggregate no
selected module renders any more. It never reads `base_sha256`. So `sync --check`
exit 0 means every generated output on disk is exactly what generation produces,
regardless of what the lock recorded — and it is blind to a hand-written file no
module generates, for which `ggg diff` is the only probe.

`generated_stale` is actionable: `ggg sync` deletes the outputs the selected
graph no longer renders. Several emitters produce nothing at all once their
input set empties, so removing the last module that declared a scenario, an API
operation, or a locale used to leave an aggregate on disk that still compiled
into the build and still referenced renderers the removal had deleted. The set
that sweep is allowed to delete is `IsRegistryOwnedOutputPath` in
`internal/modkit` — the same predicate `GenerateAll` checks every emitted path
against, so the pipeline cannot render an output the sweep would then delete.
templ, sqlc, and Tailwind outputs are deliberately outside it: this pipeline
does not render them, so their absence from a render means nothing.

## The lifecycle

### Install

```sh
go run ./cmd/ggg add component/data-table
```

`add` puts the id in `gogogadget.json` (or removes it from `exclude`) and calls
the reconciler. The plan resolves one registry commit, builds and cycle-checks
the graph, verifies every payload digest, rewrites import prefixes, allocates
migration numbers, preflights every namespace claim, and classifies each
destination — all without writing. Then `Apply` snapshots every path it can
touch, stages output, atomically replaces files, runs the generator pipeline
once, and writes the lock last. Any failure restores the exact pre-run bytes and
modes, and names any path it could not restore.

Use `--dry-run` to see the plan and stop.

### Modify

Installed files are yours. Edit them. Nothing marks a file read-only and nothing
reverts it.

```sh
go run ./cmd/ggg diff                      # what have I changed?
go run ./cmd/ggg diff component/data-table # just this module
```

`diff` prints one line per non-clean file. This is the honest inventory of your
divergence from upstream, and it is the thing `update` consults.

### Sync

```sh
make generate                                 # ggg sync --offline, then templ/sqlc/tailwind
go run ./cmd/ggg sync --check --offline       # is the tree already correct?
```

`sync` reconciles the tree to the declared intent. `sync --check` renders
everything to a temporary tree and compares bytes, failing on changed content,
missing outputs, or stale registry-owned files that the current selection no
longer produces. It is exit 4 on drift and writes nothing, which is what makes
it usable as a gate. `make check` runs it after `make generate`, so the local
gate proves there is no drift rather than asserting it.

If you edit a module's own source **in this repository**, you also have to run
`go run ./cmd/ggg registry build`, because the payload and its manifest live in
the same tree and your edit has stalled the recorded digest. Otherwise the next
sync refuses with a `sha256 mismatch`.

### Update and conflicts {#update-and-conflicts}

```sh
go run ./cmd/ggg update
```

Update advances the whole installed graph toward one commit. For each changed
file:

- **Pristine locally** — replaced automatically.
- **Locally edited, and upstream did not change it** — left alone. Not a
  conflict; your edit simply stays.
- **Locally edited, and upstream changed it too** — your bytes are **not
  touched**. The complete upstream candidate and a unified diff are staged as
  `tmp/ggg/conflicts/<run>/<module>/<hash>-<name>.candidate` and `.diff`, the
  structured metadata (base, local, and upstream digests plus both artifact
  paths) is recorded in the module's `pending` block in the lock, the module is
  marked `conflicted`, and the command exits 4.

Scope of the hold depends on `contract`: if the conflicted module's contract
changed, it plus its reverse dependents plus any required dependency whose
contract also changed stay at their old commits. Otherwise only the conflicted
module and its reverse dependents hold. Independent compatible modules update in
the same run.

Then decide, per file:

```sh
go run ./cmd/ggg diff --upstream
go run ./cmd/ggg resolve component/data-table --path internal/… --accept-upstream
go run ./cmd/ggg resolve component/data-table --path internal/… --keep-local
go run ./cmd/ggg resolve component/data-table --path internal/… --merged
```

| Mode | Bytes on disk | Lock effect |
|---|---|---|
| `--accept-upstream` | Replaced with the candidate | Recorded `clean` |
| `--merged` | Your already-merged bytes kept | Recorded `modified` against the **new** base |
| `--keep-local` | Your bytes kept | Base, revision, contract, and source commit advance; recorded `modified` |

`--keep-local` is not "ignore this forever". Advancing the base is what clears
the conflict *and* leaves a later upstream change able to conflict correctly. A
resolution that left the base behind would either re-report the same conflict
every run or go quiet permanently.

An unresolved conflict is intentionally **not portable**. `sync --check` fails
until it is resolved, so a pending candidate under ignored temporary storage can
never be committed as a green state.

### Doctor

```sh
go run ./cmd/ggg doctor
```

`doctor` answers "is this checkout coherent?" and exits 3 with findings if not.
The findings have stable codes so automation can branch on them:

| Code | Severity | Meaning |
|---|---|---|
| `project_invalid` | error | `gogogadget.json` is not canonical |
| `lock_invalid` | error | `gogogadget.lock.json` is not canonical |
| `conflict_pending` | warn | A staged conflict awaits `ggg resolve` |
| `module_pinned` | warn | Held by a pending update elsewhere in its closure |
| `candidate_missing` | error | Conflict recorded but candidate bytes absent |
| `candidate_mismatch` | error | Candidate bytes do not match their digest |
| `candidate_unreadable` | error | Candidate cannot be read |
| `candidate_path_invalid` | error | Candidate or diff path escapes the artifact prefix |
| `diff_missing` | warn | Conflict diff artifact absent |
| `owned_file_missing` | warn | A conflicted file is missing locally |
| `owned_file_unreadable` | warn | A conflicted file cannot be read |
| `conflict_stale` | warn | The file changed since the conflict was recorded |

`candidate_missing` is the case this exists for. Conflict metadata lives in the
committed lock; candidate bytes live under ignored `tmp/`. Clone such a repo and
the metadata arrives without the bytes. `doctor` names it, and rerunning
`ggg update` at the lock's target commit re-downloads and re-materializes the
candidates without touching your source.

### Remove

```sh
go run ./cmd/ggg remove component/data-table
```

Removal is the operation with the most reasons to say no, all of them before any
write. That is [its own page](/docs/module-removal).

## Authoring a module

`ggg registry validate` does two things. First it loads the catalog and reports
every id, refusing a malformed manifest or a duplicate catalog id; namespace
claim collisions are caught later, by the planner preflight, which is also a
refusal with zero writes. Then it proves the claim that data checks cannot: that
a module can actually be installed, built and taken back out. `registry/testdata`
is a second, self-contained registry publishing one example module of each kind —
a leaf element, a component that depends on it, a static page, a job-backed
workflow with a migration, and a provider-style system. For each of them the
command copies this tree into a throwaway derivative, vendors the example into
the copy's own catalog, installs the closure, generates, compiles `./...`, runs
the module's own tests, removes it, and then requires the tree to come back byte
for byte.

Two differences are tolerated after removal, and both are asserted rather than
ignored. An immutable migration stays on disk with the digest it was allocated
under, because a database that has run it cannot be told to un-run it. And each
generated aggregate's `index:`/`registry:` header still differs, because that
header is a digest of the lock and the lock legitimately still carries the
removal tombstone — the aggregate's *body* must be identical, which is where a
leftover route, translation, component entry or environment key would show.

The examples are never installable here. They live in their own registry root
that no shipped index references, so this project's catalog cannot resolve one,
no profile can list one, and nothing generated from the lock can reach them.

The loop for a new module is:

1. Write the source files where they belong in the tree.
2. Write `registry/modules/<kind>/<name>/module.json` — declare `files`,
   `claims`, `runtime`, `data`, and a `removal_policy`, leaving the digests as
   anything.
3. `go run ./cmd/ggg registry build` — refreshes every manifest digest from the
   payload on disk and rebuilds the kind indexes by scanning the tree, so a
   newly authored module becomes visible. Output is sorted and byte-stable.
4. `go run ./cmd/ggg registry validate` then `make generate`.

`registry build` derives the indexes from the tree rather than from the indexes
it writes: deriving an index from itself would make a new module permanently
invisible. It scans `registry/modules`, so it deliberately does not refresh the
example manifests under `registry/testdata`; a test recomputes those digests and
names the correct value when a payload changes.

## Where to go next

- [CLI and registry](/docs/cli) — every command, flag, envelope key, exit code.
- [Module reference](/docs/module-reference) — the generated inventory.
- [Module removal and data retention](/docs/module-removal) — policies and
  migration guarantees.
- [Architecture](/docs/architecture) — the package map the modules land in.
