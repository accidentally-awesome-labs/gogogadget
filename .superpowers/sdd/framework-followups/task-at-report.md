# Task AT — the red-proof gate: mutation proof that every guard can still be driven red

Owner: `RedProof`. Base `444c16bb` (post `EnumResidual` bump, modkit revision 80).
One new guard test (`internal/modkit/redproof_selfhost_test.go`, a `self_host` payload),
a 25-patch corpus + derived inventory under `internal/modkit/testdata/redproof/`,
27 new payload declarations in `registry/modules/system/modkit/module.json`
(revision 80 → 81, all `self_host: true`), and one added row in
`internal/gggcli/selfhost_test.go`'s `inapplicableSkipSites` (the mechanism demands
a declared reason for the gate's own `[inapplicable]` skip site — unavoidable, and
the right call rather than the exception: an undeclared site would violate the gate
this slice installs beside).

## The thesis, and the mechanism

Every guard shipped in `v0.18.0`–`v0.20.0` was driven red once, at birth. Nothing
kept it red-able: a refactor can gut a guard's assertions while its population
floors still pass; a "fix" can weaken a check into tautology; a rename can strand
it — the v0.20.0 sweep found two v0.19.0 guards already drifted one release after
shipping. Birth-red is not durability. **A guard that cannot be driven red on
demand is not a guard, it is a hope.**

The gate is patch-based mutation proof, run as an ordinary `go test`:

- a **red proof** is a unified diff planting one violation, paired with the exact
  `-run` names of the guard(s) that must catch it and the substring the guard's
  own failure text must contain;
- per patch the runner materialises a scratch copy of the tree (63 MB, ~3 s:
  the tree minus `.git`/`bin`/`tmp`/`.superpowers`/`.worktrees`/`.env`/`.ggg` at
  the root and `node_modules`/`playwright-report`/`test-results` at any depth —
  root-anchored on purpose, because a basename-anywhere rule is how `docs/` once
  ate `registry/modules/page/docs/`, and would today eat `internal/web/tmp`),
  gives it a git identity (`git init` + one commit: `TestEveryTrackedSourceFileHasAnOwner`
  derives its population from `git ls-files`, and `git apply` is the patch
  semantics the corpus stores), applies the patch, runs the paired guard, and
  **requires FAIL containing the expected substring**;
- three failure modes are named, each with the remedy in the message:
  - **TOOTHLESS** — the guard passed over its own planted violation: names the
    guard, the patch, the target files, the hunk summary, the expected substring,
    and the observed green output;
  - **ENVIRONMENTAL MASQUERADE** — the guard failed but the observed text lacks
    the expected substring ("failed, but for the wrong reason" is not a pass):
    quotes both texts;
  - **STALE** — `git apply` refused: names the patch, the target files, git's
    own reason (`patch failed: AGENTS.md:10`), and says to regenerate. A stale
    patch is the gate **working**: someone moved the code and must refresh the
    proof with it;
- after every subtest the mutant is reverted (`git apply -R`, deferred so a
  failed proof cannot spend the scratch copy for later ones) and a **RESIDUE**
  check (`git status --porcelain`) proves the tree came back byte-identical;
- before any proof runs, the **clean-tree canary** runs every paired guard
  against the unmutated copy and requires green — a guard that is red on the
  clean tree makes every red proof of it meaningless. This is not decoration:
  it caught two real problems during development (see "The canary earned its
  keep on the first day").

No patch in the corpus needs snapshot healing: every current guard reads the
bytes it asserts from the tree, not from the signed snapshot (verified per patch
empirically — manifest, schema and AGENTS.md plants all fail for the planted
reason with digests stale). The runner keeps the distinction anyway — it is the
expected substring, never luck, that separates "failed for the planted reason"
from "failed for an environmental reason". `RefreshManifestDigests` +
`WriteRegistrySnapshot` remain the sanctioned heal if a future patch targets a
snapshot-verified path.

## The corpus

25 patches over 26 distinct guards, seeded from the plants already documented in
the sweep reports and task-aq/task-ar (PLANT-T, PLANT-X, PLANT-S2, PLANT-H,
PLANT-V, PLANT-M, PLANT-W, PLANT-J, PLANT-A, PLANT-R, the GoDependency schema
plant, the url/env_key conditional, the enum-row gut, the fabricated exclusivity
claim, the second-owner collision, the orphan probe, the UNLISTED payload, the
plane-figure drift, the PCRE lookbehind, the genesis `-run` rename, the accounted-
runner guts, the deleted notify tests, the undeclared skip site). Every patch was
driven red in a scratch copy during development, its observed failure text
recorded, and the expected substring chosen from that text.

Per family (the three families the v0.20.0 sweep covered):

| family | patches | guards | proofs |
|---|---|---|---|
| web | 11 | 12 | templ.Raw XSS (PLANT-T), ui seam imports (PLANT-X), CSP grammar loosening (PLANT-S2), menuItemClass design rules (PLANT-H), AdminLayout chrome footer (PLANT-V), DropdownMenu duplicate ids (PLANT-M), Badge nested-size unstyled class (PLANT-W), hand-written PlantWidget renderer (PLANT-J, two guards), production hx-confirm (PLANT-A), stale chain comment (PLANT-R), AGENTS.md middleware-order drift |
| modkit | 4 | 4 | genesis `-run`-selects-nothing, `$defs.GoDependency` bogus-field/required-strip, the url→env_key sixteenth conditional, the enum-refusal gut |
| ownership | 10 | 10 | fabricated repo-map exclusivity claim, catalog second-owner collision, unowned tracked file, undeclared registry payload, AGENTS.md plane-1 figure drift, PCRE lookbehind in the published schema, accounted-runner Untested gut, unreasoned-marker gut, deleted package tests, undeclared skip site |

Floors (all enforced, all failing loudly): ≥ 20 patches, ≥ 15 distinct guards,
≥ 3 proven guards per family (a family with zero patches fails naming the family
— the floor is higher than one so the failure arrives before the family is
empty), patches ≥ distinct guarded tests.

## The inventory: derived, not hand-maintained

`internal/modkit/testdata/redproof/inventory.txt` — 67 guards. Derivation, run
at gate time from the tree:

- the guard population is every test matching the repository's own guard naming
  families — `TestAgents*`, `TestEvery*`, `TestNo*`, `TestPublished*`,
  `TestValidator*`, `TestThe*` — in the **guard files**: every
  `*_selfhost_test.go` under `internal/{modkit,gggcli,web,web/templates,web/templates/ui}`
  (the convention AGENTS.md's self_host paragraph states: "a new self-hosting
  test goes in a self_host payload" named just so), plus the eleven swept
  non-selfhost guard files (`csp_test.go`, `layout_chrome_test.go`,
  `designsystem_test.go`, `templ_raw_test.go`, `imports_test.go`,
  `control-id_test.go`, `contract_test.go`, `rendered_classes_test.go`,
  `ci_workflow_test.go`, `registry_test.go`, `gate_test.go`);
- cross-checked against the named contract guards (six sweep-covered guards
  whose names fall outside the families, e.g. `TestDesignSystemLayering`,
  `TestCIProfilesJobRunsTheGenesisSweep`);
- cross-checked at generation time against the sweep reports' guard lists —
  every guard family the assignment names is in the corpus (schema/model parity,
  ownership planes, `TestAgents*` document-truth, `TestNoRendererEmitsTwoIDs…`,
  the templ.Raw allowlist, the ui import boundary, the CSP grammar set-equality,
  the seven design prohibitions, the chrome-equality union-find, the
  middleware/chain-comment agreement, the accounted-runner refusals, the genesis
  `-run` gate, the exactly-one-owner catalog check).

Both directions are enforced, so neither side can drift quietly:

- **derived ⊇ inventory** — a new guard test in a guard file, or a rename, fails
  by name until it gets a patch or a stated allowance;
- **inventory ⊇ derived** — a deleted or renamed guard leaves a stale line that
  names what went missing (and its patch goes stale beside it);
Every inventory line names either a patch that exists and whose `Run` names
that guard (one line per guard — two patches claiming one guard is an error),
or carries `allow:<reason>`. 26 guards are proven; 41 carry allowances.
Shrinking the inventory is a visible, deliberate diff — the floors above still
hold underneath it.

**The 41 allowances follow one rule: an allowance is legitimate only if it
names the guard's sweep verdict, the patched guard that compensates its
documented hole, or the population it walks — a bare "later" is exactly the
hand-table drift this gate exists to prevent.** Concretely, three classes:

1. *sweep verdict SOUND, plant not transcribed* — the sweep drove these red at
   birth with a control and the verdict is quoted in the allowance (e.g.
   `TestAgentsSourcePlaneNamesItsGuard`: "document names the test AND the test
   declares the func"). The patch is transcription work, not investigation.
2. *declaration-derived, compensated* — the sweep's UNDER-SCOPED /
   DECLARATION-DERIVED rows whose escape is caught by a **patched** guard; the
   allowance names both (e.g. `TestEveryExportedRendererTakesOneOptionsStruct`
   points at `web-plantwidget`, the PLANT-J patch; the three size/kind matrices
   point at `web-badge-xxl`, the rendered-output universal that closes PLANT-W).
3. *documented hole, widening unwritten* — rows where the sweep proved the
   escape (PLANT-Z, PLANT-N, PLANT-O, PLANT-P) and the honest red is the
   widening patch itself; the allowance says so rather than pretending
   coverage.

Allowances persist across regeneration (the generator carries them forward
verbatim, so a vanished reason is a deliberate edit), making the reasons as
durable as the inventory.

Regenerate: `GGG_REDPROOF_UPDATE_INVENTORY=1 go test ./internal/modkit -run TestRedProofGate`
(writes the file and fails the run so the diff gets reviewed; regenerating is the
documented remedy in every inventory-related failure message).

Honest boundary, stated: a guard added to a *new* file is invisible to the
derivation until that file is added to the enumerated list. The selfhost glob
covers the repository's stated convention; the eleven non-selfhost files are a
fixed list. A brand-new non-selfhost guard file escapes until enumerated — the
sweep reports remain the external oracle for that case.

## Wiring

- Runs as an ordinary `go test` (so `make check`'s accounted suite exercises it),
  as `TestRedProofGate` in `internal/modkit`.
- A `self_host: true` payload (with the whole corpus): derivatives never receive
  it — a corpus pointing at core-only paths would not apply there. Belt and
  braces, the runner also refuses to run when the module path is not the
  registry's `canonical_module`, the same discriminator `InstallsSelfHostPayloads`
  uses.
- Skippable by env when scratch copies are impractical: `GGG_REDPROOF=off`
  (default: run). The skip carries the `[inapplicable]` marker with a reason and
  is declared in `inapplicableSkipSites` — the accounted gate's own inventory of
  who may exempt themselves, which is exactly why the row was unavoidable.

## Measured runtime

Full suite (scratch copy + clean-tree canary over 24 distinct package/pattern
pairs + 25 mutation proofs + reverts), measured on this machine (M1 Max):

```
ok  github.com/gogogadget/gogogadget/internal/modkit  91.829s
```

~1.5 minutes, against the 10-minute budget, with no scoping compromise: the
whole tree is copied because the ownership/planes/catalog guards assert over
all of it. The cost stays low because the scratch shares `GOCACHE` (content-keyed,
so unmutated packages hit) and because only the paired `-run` pattern executes
per proof — `internal/web`'s live-DB suite never runs. Sequential by design:
one scratch, apply → prove → revert, so residue is detectable.

## The meta-gate, driven red (all three quoted)

**(a) delete a patch → the floor fails naming the uncovered guard.** With
`web-templ-raw.patch` removed from the corpus:

```
inventory says guard TestEveryTemplRawCallSiteIsJustified is proved by patch
web-templ-raw, and no such patch is in internal/modkit/testdata/redproof —
restore it or drop the line in the same change
```

**(b) pair a patch with a guard that does not catch it → toothless fires with
both texts.** `web-menuitem-design`'s `Run` retargeted to
`TestAgentsRepoMapExclusivityClaimsHold` — a genuinely unrelated guard — with
the inventory line made consistent so coverage stays green and the failure
isolates:

```
TOOTHLESS: guard TestAgentsRepoMapExclusivityClaimsHold PASSES over the violation
patch web-menuitem-design plants in internal/web/templates/ui/shared.go (PLANT-H:
menuItemClass … five of the seven prohibitions on every menu item in the catalog …).
Expected the guard's own failure text to contain "design-system violation"; the
mutant stayed green instead. …
observed (green) output:
ok  	github.com/gogogadget/gogogadget/internal/web/templates	0.164s [no tests to run]
```

Note what the green output shows: the retargeted pattern selected no test in that
package and `go test` exited 0 — the exact vacuity (a gate green because it ran
nothing) that the toothless check plus the exact-name `Run` convention exists to
prevent.

**(c) corrupt a patch's context lines → stale fires naming patch, target, reason.**
One trailing context line in `own-planes-figure.patch` altered so `git apply`
refuses:

```
STALE: patch own-planes-figure no longer applies to AGENTS.md.
error: patch failed: AGENTS.md:10
error: AGENTS.md: patch does not apply
The code the proof was pinned to changed — regenerate the patch against the
current tree (plant the violation in a scratch copy, git diff, restore the
Redproof-* headers) in the same change that moved the code. A stale proof is the
gate working, not the gate failing.
```

All three demonstrations were reverted; the corpus in the commit is the proven
green set.

## The canary earned its keep on the first day

Two clean-tree canary failures fired during development, both real, both
bootstrap-order artifacts of this very slice:

1. `TestEveryTrackedSourceFileHasAnOwner` red on the clean scratch — the runner
   and corpus were not yet declared in the manifest, so the scratch's ownership
   guard saw unowned files. The gate refused to certify any proof over a dirty
   tree until the manifest declared them.
2. `TestEveryInapplicableSkipSiteIsDeclared` red on the clean scratch — the
   runner's own `[inapplicable]` skip site was undeclared. The row in
   `inapplicableSkipSites` is the fix, not an exemption.

That is the design working: the red-proof gate holds itself to the guards beside
it before it certifies anything.

## Files

```
internal/modkit/redproof_selfhost_test.go          new, self_host payload — the gate
internal/modkit/testdata/redproof/*.patch          new, 25 self_host payloads — the corpus
internal/modkit/testdata/redproof/inventory.txt    new, self_host payload — derived inventory
internal/gggcli/selfhost_test.go                   +1 inapplicableSkipSites row
registry/modules/system/modkit/module.json         +27 payload declarations, revision 81
.superpowers/sdd/framework-followups/task-at-report.md  this report
```

`registry.snapshot.json` / `registry.snapshot.sig` / lock digests are stale for
the manifest change by design — the release order rewrites them, and it is
Main's to run once this slice lands.

## Gates

| gate | result |
|---|---|
| `go test ./internal/modkit -run TestRedProofGate` | **pass** — `ok … 91.829s`, 25/25 proofs red-then-reverted, clean canary green |
| `go test -race ./internal/modkit` | see final yield (run below) |
| `go test -race ./internal/gggcli` | see final yield |
| `go vet ./internal/modkit` | clean |
| `make check` (runner active) | run after the release order refreshes the snapshot — quoted in the final yield |
| `bin/ggg registry validate` | same |

Not pushed, not tagged. The sweep reports were read and left untouched.
