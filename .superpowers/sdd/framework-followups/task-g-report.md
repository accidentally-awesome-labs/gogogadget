# Task G report — a stale `bin/ggg` now rebuilds, and refuses when it cannot

Commit: `148cc93` — `fix(cli): rebuild a stale bin/ggg and refuse a lock from a newer engine`
(single commit; worktree clean, `ggg sync --check --offline` clean).

## G1 — the `Makefile` prerequisite decision

`Makefile:9-27` (owned by `ggg/system/project-base`):

```make
GGG_SOURCE := $(shell go list -deps -f '…' ./cmd/ggg 2>/dev/null | sed -e 's|^$(CURDIR)/||')

$(GGG): go.mod go.sum $(GGG_SOURCE)
	go build -o $(GGG) ./cmd/ggg
```

The template selects packages whose `.Module.Path` is this module and emits
`{{.GoFiles}}` plus `{{.EmbedFiles}}` per package.

**Why `go list -deps` and not `find cmd/ggg internal/gggcli internal/modkit`.**
The brief's three directories are a floor, not the set. `go list -deps ./cmd/ggg`
reports twenty-one first-party packages: beyond those three it reaches
`internal/remote`, `internal/provision/neon`, `internal/deploy/fly`,
`internal/database/ops/docker`, `internal/gggcli/ui`, the generated
`internal/gggcli/commands`, and through it `internal/config`, `internal/db`,
`internal/db/sqlc`, `internal/identity/*`, `internal/billing`, `internal/audit`,
`internal/notify`, `internal/telemetry`, `internal/apphost`. A hand-written
`find` list would have silently missed a change in any of them — which is the
same class of bug as the one being fixed — and would need editing every time a
module adds a provisioner or deployer. `.GoFiles` also excludes `_test.go` for
free.

Three deliberate details:

- **Immediate `:=`, no `.SECONDEXPANSION:`.** One `go list` (~0.4 s) at parse
  time on every `make` invocation, including `make help`. Deferring it with
  secondary expansion would save that on targets that never build the binary,
  at the cost of a make feature nobody reading this Makefile expects. Boring won.
- **`2>/dev/null`, failure degrades to no prerequisites.** In a fresh genesis the
  sqlc and command-registry packages do not exist yet, so `go list` cannot
  resolve the graph. An empty list means "build it if it is absent", which is
  exactly right there: `ggg setup` builds `bin/ggg` itself with a fixed
  `go build` and does not consult this variable.
- **`sed -e 's|^$(CURDIR)/||'`.** `go list` prints absolute paths and make splits
  prerequisites on whitespace, so an absolute path under a directory whose name
  contains a space would break the list for a consumer. Project-relative paths
  are immune to that.

`go.mod`/`go.sum` are explicit prerequisites: a dependency version bump changes
the binary without changing any first-party file.

Proof:

```
$ go build -o bin/ggg ./cmd/ggg && make -n check
bin/ggg check                       # up to date: no rebuild

$ touch internal/modkit/model.go && make -n check
go build -o bin/ggg ./cmd/ggg       # rebuild first
bin/ggg check
```

## G2 — where the constant and the comparison live, and why

### The constant: `internal/modkit/contract.go`

```go
const EngineContract = 2
```

`contract.go` is the file that already declares the CLI's public contract —
`ProjectFileName`, `LockFileName`, `Envelope`, and `ExitOK…ExitRollback`. The
engine contract is the same kind of thing: a number automation and operators
depend on, produced by this package and consumed by `gggcli`. The doc comment
carries the bump rule and one line per contract value:

```
1 — provider-aware schema 2: scoped ids, provider slots, runtime orders.
2 — the runtime.health capability: locks assume a health provider exists.
```

Value `2` because task F's `runtime.health` capability provider is precisely
the change that makes an older binary wrong — the observed failure.

**Why not `Lock.Schema`.** The brief asked me to say so either way. `Lock.Schema`
is the wrong carrier, for two independent reasons:

1. **Different comparison.** `Schema` is validated `== 2` (`validate.go:1207`,
   `lock.schema.json` `"const": 2`) and a mismatch in *either* direction refuses
   and points at `ggg migrate-schema-1`. The engine contract must be asymmetric:
   newer refuses, equal and older proceed. Overloading `Schema` would either
   break the "older proceeds" rule or destroy the "this file format changed"
   meaning.
2. **Different cause.** The lock's byte format did not change in task F; only the
   behavior a reader must implement did. Bumping `Schema` for that would demand a
   migration command for a file that needs none, and would make every future
   capability addition look like a format break to the published JSON schema.

So `Lock` gains one sibling field, `EngineContract int` (`model.go:837-848`,
`engine_contract` in `lock.schema.json`), documented as "Schema versions the file
format; this versions the behavior the resolved state assumes of its reader".
No second vocabulary: `contract` is already this repository's word for "callers
must change" (`Manifest.Contract`, `Requirement.Contract`, `ContractBounds`), as
against `revision` for "implementation changed".

### The comparison: one function, five call sites

```go
// internal/modkit/contract.go
func EngineContractRefusal(recorded int) error {
	if recorded > EngineContract {
		return EngineContractError{Lock: recorded, Binary: EngineContract}
	}
	return nil
}
```

Single implementation so no caller can get the direction wrong. `EngineContractError`
carries `ExitCode() int → ExitRefusal`, so the code travels with the error.

- **`ParseLock` (`codec.go:66`)** is the primary gate: it is the one function
  every lock read in the tree passes through (`resolve.readPlannerInputs`,
  `health.Report`, `example.go`, and eight `gggcli` sites). The guard sits after
  decoding and before `validateLock`, so a stale engine is named before any
  downstream validation noise, and long before the planner runs — nothing is
  written.
- **`MarshalLock` (`codec.go:119-122`)** stamps `clone.EngineContract =
  EngineContract` rather than trusting the caller. The lock records *the engine
  that wrote it*; no construction site (and no test fixture) can claim a
  contract it does not implement, and the ~45 `Lock{Schema: 2, …}` literals in
  the tree needed no edits.
- **`MigrateSchema1Lock` (`migration_v2.go:155-156`)** stamps it too — the
  migrating binary is the writer.
- **`previewTrustedTask` (`tasks.go:78-98`)** refuses before apply. `setup`,
  `check`, `generate`, `dev`, `db`, `services` reach their first lock read only
  inside `applyTrustedTask`, which creates `.ggg/env/{development,test}.env`
  first. Gating at preview keeps the preview/apply contract intact: a stale
  engine writes nothing at all, not even an empty env file.
- **`collectDiff` (`executors.go:251-260`)**, **`readCatalog`
  (`executors.go:460-474`)**, **`registryAddPreview` (`registry_authors.go:333-343`)**
  each explicitly re-raise it. The first would have flattened it into
  `usageError(err.Error())` (a string, losing the code and calling it bad input);
  the other two swallowed every parse error to stay browsable before install.
- **`gggcli.ExitCode` (`contract.go:18-34`)** lets the refusal outrank the code
  of whichever layer reported it, and `failureEnvelope` now delegates to
  `ExitCode` instead of repeating the `errors.As` walk, so the envelope's `exit`
  and the process status cannot disagree. This is load-bearing for the trusted
  tasks (`applyTrustedTask` labels step failures `runtimeError`) and for the
  remote command planners (`plannerFailure{runtimeError(err)}`).
- **`health.Report` (`health.go:94-110`)** reports it as a distinct
  `engine_stale` finding instead of `lock_invalid`/"not canonical". The lock is
  fine; the binary is not, and telling an operator their lock is corrupt sends
  them looking for the wrong thing.

### `engine_contract` is `omitempty`, and absent means contract 0

Deliberate, and the one place I departed from the lock's otherwise fixed key set.
Every lock `MarshalLock` writes records the field (the stamped value is never 0,
and `lock.schema.json` bounds it `minimum: 0` with the Go twin refusing negatives),
so a *written* lock's key set is still fixed. It is `omitempty` purely so a lock
written before the guard existed reads as contract 0 — the oldest contract there
is, which the brief requires a newer binary to accept silently. Making it required
would have refused every pre-existing lock with `parse lock: lock field
"engine_contract" is required`: an unactionable engine error, i.e. the exact
failure mode this task exists to remove, pointing the other way. It also would
have been unbootstrappable here without hand-editing the committed lock, which
the rules forbid. Mutation M4 below holds this decision in place.

## The refusal cases and their proving mutations

Tests: `internal/modkit/model_test.go` (`TestParseLockRefusesOnlyANewerEngineContract`,
`TestMarshalLockStampsThisEnginesContract`) and `internal/gggcli/cli_test.go`
(`TestCLIRefusesALockWrittenByANewerEngine`, `TestStaleEngineRefusalOutranksItsReporter`).

Each mutation was applied to the fixed tree, the four tests run, and the tree
restored. Baseline: `exit 0`.

| # | Mutation | Symbol | Fails |
|---|---|---|---|
| M1 | `if false && recorded > EngineContract` (guard never fires) | `modkit.EngineContractRefusal` | `newer_refuses_with_the_remedy`: `ParseLock error = <nil>, want EngineContractError`; `[sync --offline] = nil error, want a stale-engine refusal` |
| M2 | `>=` for `>` (off-by-one) | `modkit.EngineContractRefusal` | `equal_proceeds`: `ParseLock(engine_contract 2): … records engine contract 2; this ggg binary is contract 2`; also `TestMarshalLockStampsThisEnginesContract` |
| M3 | `!=` for `>` (symmetric) | `modkit.EngineContractRefusal` | `absent_proceeds` and `older_proceeds`: contract 0 and 1 both refused against binary 2 |
| M4 | drop `,omitempty` (field required) | `modkit.Lock.EngineContract` | `absent_proceeds`: `parse lock: lock field "engine_contract" is required` |
| M5 | drop the `errors.As` branch | `gggcli.ExitCode` | `TestStaleEngineRefusalOutranksItsReporter`: `runtime-wrapped step exit = 1, want 3` |
| M6 | `return nil` instead of the gate | `gggcli.previewTrustedTask` | `preview "setup" on a newer lock = nil error, want a stale-engine refusal` |

M1–M3 are the three brief cases (newer refuses / equal proceeds / older
proceeds). M4–M6 hold the three design decisions that make the refusal actually
land: absent-tolerance, exit 3 through any reporter, and no write before the
refusal.

`TestCLIRefusesALockWrittenByANewerEngine` additionally asserts, on a real
project root through `App.Run`:

- `sync --offline`, `sync --check --offline`, `diff`, `catalog`, `doctor`, and
  `provider list` all exit 3 with `gogogadget.lock.json` and
  `go build -o bin/ggg ./cmd/ggg` on the surface the operator sees (error text
  plus rendered output — `doctor` reports through findings);
- `setup`, `check`, `generate` refuse at **preview**;
- a `treeDigest` over every file under the root is byte-identical before and
  after all of it — nothing was written;
- an older lock (`engine_contract: 1`) runs `diff` with exit 0 and no `engine
  contract` text on stderr, and `sync --offline` succeeds and re-stamps it to 2.

### Reachable from the real CLI, not only a unit test

Against this repository with `./bin/ggg`, contract mutated in the real lock and
restored afterwards:

```
$ sed '/"engine_contract": 2,/d' lock.bak > gogogadget.lock.json   # a pre-guard lock
$ ./bin/ggg diff                       ; exit=0   (silent)
$ ./bin/ggg sync --offline             ; exit=0   update lock  → re-stamped to 2

$ sed 's/…: 2,/…: 3,/' lock.bak > gogogadget.lock.json            # a newer engine
$ ./bin/ggg diff                       ; exit=3
error: gogogadget.lock.json records engine contract 3; this ggg binary is contract 2.
Rebuild the CLI from this tree — `go build -o bin/ggg ./cmd/ggg`, or `make setup` — then re-run
$ ./bin/ggg sync --check --offline     ; exit=3   error command_failed <same message>
$ ./bin/ggg doctor                     ; exit=3   error engine_stale   <same message>
$ ./bin/ggg catalog                    ; exit=3   <same message>
```

## Docs

`content/docs/cli.md` (owned by `ggg/system/content-assets`) gains a
**The engine contract** section between "The two files" and "Commands": why a
project-built CLI can fall behind, the recorded field, the `schema` vs
`engine_contract` split, the three-row lock-vs-binary table, the verbatim
refusal, the `engine_stale` doctor finding, the make prerequisite, and the bump
rule for contributors. The exit-3 paragraph under "Exit codes" now names the
stale-engine refusal and links to it. Everything stated is implemented; there is
no aspirational text.

## Verification

- `make check` — **exit 0** (222 s). First line is `go build -o bin/ggg ./cmd/ggg`,
  i.e. G1 firing, then `bin/ggg check` → generate, `sync --check --offline`,
  `go vet ./...`, `go test ./...` (all packages `ok`, `internal/web` 84 s with
  `TEST_DATABASE_URL` on localhost:5432), `go build ./...`.
- `go test ./internal/modkit ./internal/gggcli -count=1` — ok 7.6 s / 0.5 s.
- `gofmt -l internal/modkit internal/gggcli` — empty. `go vet` on both — clean.
- `go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline && go run ./cmd/ggg sync --check --offline` — clean.
- Not run, per the brief: `make e2e`, `make visual`, `make docker-build`.

## Concerns

1. **The guard cannot help a binary built before it.** A `bin/ggg` predating this
   commit reads the new lock with `DisallowUnknownFields` and reports
   `parse lock: json: unknown field "engine_contract"`. It refuses rather than
   misreports, which is the safe direction, but the message is not actionable.
   This is unavoidable and one-time: the key now exists, so every future bump
   changes only the integer and lands on the actionable path.
2. **The bump is a judgement, not a detector.** Nothing enforces that a change
   which makes an older binary wrong also raises the constant. The rule is
   recorded beside the constant and in `content/docs/cli.md`; a contributor who
   ignores it reintroduces exactly the failure this task fixed. A cheap partial
   detector would be a test asserting the constant changes whenever the set of
   generated capability names changes — I did not add it because it would fire
   on harmless additions and train people to bump reflexively.
3. **`Controller.Redactor` (`controller.go:135-156`) still swallows the refusal.**
   It has no error channel and only registers secret env keys for masking, so a
   stale-engine lock means less redaction, not a wrong result — and every command
   that reaches Redactor also reaches a real lock read that refuses. Giving it an
   error return would have rippled through every renderer for no behavioral gain.
4. **`make` now shells out to `go list` at parse time.** ~0.4 s on every
   invocation, including `make help`, and it is the only place the Makefile does
   more than alias `bin/ggg`. If that ever grates, `.SECONDEXPANSION:` moves the
   cost onto the targets that actually build the binary.
5. **`make check` rebuilds `bin/ggg` on most runs.** `bin/ggg check` regenerates
   files under `internal/` that are in the dependency set (sqlc output, the
   command registry, `*_templ.go`), so the binary is stale again by the next
   invocation. A precise `find`-based list has the same property, since those
   generated packages are genuinely compiled into the CLI. The cost is one cached
   relink; the alternative is a rebuild that misses real changes.
