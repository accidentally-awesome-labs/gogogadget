# Task AU — the verification pipeline restructure: CI split + un-gating + cancellation, the genesis-sweep trigger, the budget line, and the gate budget rule

Owner: `GateBudget`. Base `fd5c8bf0` (v0.21.0, clean, pushed, CI green on nine jobs).
Three modules, one commit set, no release order run — digests deliberately stale
(see "Digests left stale" below); the parent owns `registry build`/`sign`/`sync`
and the revision bumps for `ggg/system/ci-github`, `ggg/system/modkit` (the CLI
module) and `ggg/system/project-docs`.

## What changed

1. **`.github/workflows/ci.yml`** (`ggg/system/ci-github` payload)
   - `test` keeps `bin/ggg test integration --race` — race is the semantic gate —
     plus generate/drift/vet/build. The accounted-runner comment stays on the step.
   - New parallel **`cover`** job: `bin/ggg test integration --cover` (no race),
     absorbing govulncheck and Fuzz from `test`. It carries its own
     TEST_DATABASE_URL env + postgres service with the scoping comment adapted to
     name both suite jobs (`test`, `cover`) and still warn the registry jobs away
     from a database nobody asked them for.
   - The seven independent jobs (`e2e`, `visual`, `smoke`, `docker`,
     `registry-core`, `registry-external`, `profiles`) lost `needs: test`.
     Verified before relying on it: every job runs its own `actions/checkout`;
     the only artifact steps anywhere are failure-report *uploads*; no job reads
     another's output. A comment at `e2e` states the reasoning.
   - Workflow-level `concurrency: group ${{ github.workflow }}-${{ github.ref }}`,
     `cancel-in-progress: true`, with the wall-time-vs-compute trade stated in a
     comment (cancellation + `make check` before every push is what makes
     parallel-start cheaper than fail-fast).
   - Every prior step survives exactly once across the two suite jobs; nothing
     else in any job changed.

2. **Genesis-sweep trigger** (`internal/gggcli/gate.go` + `tasks.go`, CLI module)
   - `runCheck` now opens with `refuseUnsweptShippedPayloadDiff`: the working
     tree's diff against `git merge-base HEAD origin/main` is compared to the
     **shipped-path set** — every non-`self_host` payload source the lock's
     installed modules declare, plus every module manifest and profile
     declaration the catalog publishes (`registry/modules/<kind>/<name>/module.json`,
     `registry/profiles/<name>.json`; profiles are catalog-level — three of the
     four shipped ones are installed by nobody, and the sweep walks
     `catalog.Profiles`).
   - A hit is a refusal (exit 3) naming each file with its owner and the exact
     remedy line. **Not** an auto-run: the sweep needs the network and ~93 s; a
     refusal is cheaper, teaches, and names the files. The refusal is lifted by
     the same env var that runs the sweep — `GGG_GENESIS_SWEEP=1 ggg check`
     executes the sweep inside the accounted suite instead of refusing (stated
     in the message).
   - Degrades are stated, never silent: no lock / no modules → skip; a tree
     whose catalog will not load (every derivative) → skip naming it; no
     origin/main merge-base (fresh clone, closed tree, no git) → skip naming it.
   - **Floor**: the derivation refuses (`collapsedShippedPaths`) when either
     count falls below the lock's module count — measured today 293 locked
     modules → 1,297 payload paths and 297 declarations — so a collapsed set can
     never be vacuously green.

3. **Slowest-packages line** (`gate.go`): the accounted summary prints
   `slowest <pkg> <Ns> ×3` directly after the totals line, from the per-package
   elapsed times the event stream already carried. Totals line shape unchanged.

4. **`AGENTS.md`** (`ggg/system/project-docs` payload): new **Gate budget**
   section beside Definition of done — today's measured figures, the standing
   rules (every gate states its measured cost; `make check` < ~5 m; CI wall
   < ~15 m; over-budget moves tier or displaces), and the shipped-payload rule
   stated as `ggg check` enforcing it. The `make check` bullet now names four
   refusals (the trigger is (4)) and the `slowest` line. Self_host paragraph:
   56 → 57 payloads, 25 → 26 mutation patches (`own-genesis-refusal.patch`).

5. **Red-proof corpus**: `own-genesis-refusal.patch` (family ownership) proves
   `TestNoUnsweptShippedPayloadDiffPassesCheck` red by gutting the refusal arm
   (`if len(hits) == 0 || true`). Inventory regenerated; it was cheap — the
   guard was born inside a guard file with its patch in the same change.

## Budget table

| Gate | Before | After |
|---|---|---|
| CI `test` job | **measured** 883 s (`--race --cover` alone 663 s = 75 %) | **projected** 9–10 m (race only; cover moved out) |
| CI `cover` job (new) | — | **projected** 5–7 m (cover + fuzz + govulncheck, parallel) |
| CI green wall | **measured** 24m41s (7 jobs chained behind `test`; critical path test→e2e) | **projected** 10–11 m = max(test-race, e2e ≈ 10.5 m, cover); **to be measured by the parent on the next push — no push was made from this slice** |
| Superseded push | full nine-job matrix burned to completion | cancelled at the next push (`cancel-in-progress`) |
| Local `make check` | **measured** 186 s (web 160 s, modkit 136 s of which ~91 s red-proof, nothing else > 37 s, ~2.3× parallelism) | trigger adds one merge-base + one `git diff --name-only` (**measured**: the refused `make check` returned in 5.1 s including the bin/ggg rebuild). Full run in the healed scratch: **measured** 246 s *with the 4-profile sweep inside* (`GGG_GENESIS_SWEEP=1`); on a clean committed tree the machinery is unchanged |
| Genesis sweep coverage | opt-in only (`GGG_GENESIS_SWEEP=1` / CI `profiles` job); a payload diff shipped un-swept in v0.20.0 and broke derivative compile | **measured**: `ggg check` refuses a shipped-payload diff in milliseconds, names the files (real-tree run below) |
| Suite size | 2166 tests (v0.21.0) | 2181 counted (`tests: 2181 passed, 0 skipped, 0 inapplicable, 1 failed across 91 packages, 16 with no test files` — the 1 failure is the scratch-heal artifact, below) |

Wall-time figures for CI are **projected, not measured**: the push is the
parent's, and the workflow has no `workflow_dispatch` trigger so `gh workflow
run` cannot invoke it on a branch (verified: 0 occurrences; `gh` is
authenticated). What *is* verified locally: the YAML parses (python yaml and the
Go guards' `yaml.Unmarshal`), all nine jobs are present, and the new guards
catch every regression shape (below).

## Verification (all quoted from this machine, today)

**CI restructure, driven red four ways** (`internal/modkit/ci_workflow_test.go`):

- drop `--cover` from the cover job → `cover: the suite command "bin/ggg test integration" dropped --cover`
- re-merge `--race --cover` in the test job → `test: the suite command "bin/ggg test integration --race --cover" carries --cover, which belongs to the other half of the split`
- re-gate any job on `needs: test` → `job e2e needs [test]; no job in .github/workflows/ci.yml gates on another …` (workflow-level via `readCIWorkflow`, so it reaches jobs no per-job assertion visits — the first per-job version missed e2e and the mutation caught it)
- drop `cancel-in-progress` → `declares a concurrency group without cancel-in-progress …`

Clean tree: `go test ./internal/modkit -run TestCI` green. Guard updates:
`TestCITestJobRunsTheAccountedSuiteUnderRace` (race pinned present, cover pinned
ABSENT), new `TestCICoverJobRunsTheAccountedSuiteUnderCover` (inverse pins +
TEST_DATABASE_URL + postgres service), new `TestCIWorkflowCancelsSupersededRuns`.

**Genesis trigger, the three demonstrations:**

1. *Shipped-payload edit refuses naming the file* — the real tree, `make check`:

```
  error    command_failed this diff changes 6 path(s) whose bytes reach derivatives, and nothing has proved the shipped profiles still create a sync-clean project:
  .github/workflows/ci.yml  (payload of ggg/system/ci-github)
  AGENTS.md  (payload of ggg/system/project-docs)
  internal/gggcli/gate.go  (payload of ggg/system/modkit)
  internal/gggcli/gate_test.go  (payload of ggg/system/modkit)
  internal/gggcli/tasks.go  (payload of ggg/system/modkit)
  registry/modules/system/modkit/module.json  (declaration of ggg/system/modkit)
The genesis sweep is opt-in because it needs the network and ~93 s, which is how a derivative-compile break reached a green local gate in v0.20.0. Run it before pushing:
  GGG_GENESIS_SWEEP=1 go test ./internal/gggcli -run TestEveryShippedProfileCreatesAProjectThatIsSyncClean -count=1
The refusal stands on every re-run of `ggg check` over this diff by design. It is lifted by GGG_GENESIS_SWEEP=1, which runs the sweep inside the accounted suite instead of refusing.
failed (exit 3)
```

2. *Source-only edit passes* — two ways. The guard subtest edits a `self_host`
payload and an unowned file over a real committed repo with a pushed origin and
asserts nil error (`a source-only diff passes`, green). And inside the refusal
above: this diff also edits `internal/modkit/ci_workflow_test.go`,
`internal/modkit/testdata/redproof/inventory.txt` and adds
`own-genesis-refusal.patch` — all `self_host` — and the refusal correctly does
NOT name any of them.

3. *No origin degrades to a stated skip* — live, from a tree with no `origin`:

```
genesis sweep trigger skipped: no origin/main merge-base to diff against — a fresh clone, a closed tree, or no git (exit status 128; its last 1 line(s) of output: fatal: Not a valid object name origin/main)
```

and `ggg check` proceeded (generation ran). The floor is driven red by the
`a collapsed derivation refuses rather than passing vacuously` subtest (a lock
whose every payload is self_host → `the shipped-path derivation collapsed: 1
locked modules produced 0 payload path(s) and …`).

**A real flaw the full-suite run caught:** the guard was first
environment-sensitive — ambient `GGG_GENESIS_SWEEP=1` (exactly the env of the
documented completing run) hollowed out its own refusing subtests. Fixed by
`t.Setenv(genesisSweepEnv, "")` in the four refusing/skip subtests; verified
green with and without the ambient env.

**Slowest line, asserted and quoted** — from a full accounted run:

```
tests: 2181 passed, 0 skipped, 0 inapplicable, 1 failed across 91 packages, 16 with no test files
slowest web 216s modkit 187s gggcli 180s
```

`TestSummaryNamesTheSlowestPackages` pins the totals line's shape by regexp
(`^tests: [0-9]+ passed, … across [0-9]+ packages$`), the budget line directly
after it, the elapsed order (not alphabetical), and the fourth package's
absence — on both the `withPackages` and refusal paths.

**Gates:**

- `go test -race ./internal/gggcli ./internal/modkit -count=1` on this tree:
  - gggcli: FAIL only `TestCommittedSnapshotVerifiesUnderThePinnedCoreKey` —
    `registry snapshot payload "registry/modules/system/ci-github/module.json"
    digest mismatch; remedy: ggg registry build && ggg registry sign …` (the
    test prints the parent's reserved step itself).
  - modkit: FAIL only `TestCoreRepositoryInstallsEverySelfHostPayload` (the new
    patch is manifest-declared but not yet a lock row — sync adds it),
    `TestEveryShippedProfileResolvesIntoACoherentProject`,
    `TestShippedProfileGateCatchesABrokenProfile`,
    `TestTheDocumentedProfileTableMatchesWhatTheProfilesResolveTo` (all
    directory-snapshot digest mismatch — same remedy).
  - Every failure is the designed pre-release-order staleness; none touches
    logic added here.
- **Healed + absorbed scratch** (tree copied to /tmp, `RefreshManifestDigests` +
  `WriteRegistrySnapshot` + one `generate` absorb — the red-proof-sanctioned
  "consistent copy without the release order"; the repo itself untouched):
  `GGG_GENESIS_SWEEP=1 make check` runs generate → drift refusal →
  `sync --check --offline` → vet → accounted suite **with the 4-profile genesis
  sweep inside** → build, and is green apart from
  `TestCommittedSnapshotVerifiesUnderThePinnedCoreKey` — the scratch snapshot is
  unsigned by construction (no local signing key; `.ggg/` carries only `env/`),
  an artifact of the heal, not of these changes.
- Red-proof gate: `red proof: 26 patches over 27 guards in 1m37.318s` — ok
  (includes `own-genesis-refusal` clean-pass and red-pass).
- `bin/ggg registry validate` on this tree: **exit 0**, 12 closures verified
  (install, compile, test, restore byte-for-byte) — and exit 0 in the healed
  scratch too.
- `go test -race` gggcli suite re-run after the env-pin fix: ok.

## Digests left stale (deliberate)

The release order was not run. Stale against `HEAD`, for the parent's
`registry build && registry sign && ggg sync --offline` + revision bumps
(expected: `ci-github`, `modkit`, `project-docs`):

- `ggg/system/ci-github`: `.github/workflows/ci.yml`
- `ggg/system/project-docs`: `AGENTS.md`
- `ggg/system/modkit`: `internal/gggcli/gate.go`, `internal/gggcli/gate_test.go`,
  `internal/gggcli/tasks.go`, `internal/modkit/ci_workflow_test.go`,
  `internal/modkit/testdata/redproof/inventory.txt`; plus the manifest's new
  `self_host` entry for `own-genesis-refusal.patch` (declared with its correct
  sha256 so the ownership sweep holds; the lock's module rows catch up at sync).
- `registry.snapshot.json` / `.sig` and `gogogadget.lock.json` accordingly.

## Post-land expectations for the parent

1. Release order + revision bumps (three modules), then `make check` should be
   green on the committed tree with no scratch needed.
2. Push and read the CI wall: projected 10–11 m. If the green wall lands above
   ~15 m, the Gate budget rule already names the move (tier or displace).
3. The first payload-touching push after this lands will *by design* get the
   refusal locally until the sweep runs — that is the v0.20.0 door closing.
