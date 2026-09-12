# task-be — the revision gate's blind direction

A module whose bytes differ from the last RELEASED snapshot must carry a
revision higher than the one it published there. Everything quoted below was
run in this tree; nothing is projected except the two CI figures explicitly
labelled as such.

## 1. The mechanism, in one sentence

**Both existing halves compare a manifest against an artifact the same workflow
rewrites — the lock (`ggg sync` refreshes it) and the payload bytes on disk
(`registry build`'s own digest refresh rewrites them) — so a single edit that
moves a payload AND refreshes the digest recorded for it leaves both sides
agreeing again at the old revision, and the gate has nothing left to compare.**

The gate as it stood, quoted (`internal/modkit/registry_build.go`):

```go
	return fmt.Errorf(
		"payload digests changed without a revision bump: %s. revision is the module's version of record — it feeds indexSHA and `ggg info` — so an implementation change that leaves it alone publishes a lie. Bump revision (contract moves only when a consumer must change code)",
		strings.Join(stale, ", "))
```

and the two comparisons that feed it:

```go
		if document.Module.Revision != locked.Revision {
			continue
		}
		if manifestPayloadDigest(document.Module) != lockedPayloadDigest(locked) {   // manifest vs LOCK
```

```go
		if pinned, listed := published[manifest.path]; !listed || pinned != digestBytes(manifest.raw) {
			// The manifest moved since the last published snapshot, so
			// the revision may have moved with it: not provably stale.
			continue
		}
		if manifestPayloadsMoved(root, manifest.document.Module) {                    // manifest vs DISK
```

Both are two-point comparisons between two MUTABLE points. Note the second
one's `continue`: a manifest whose bytes moved is deliberately let through,
because "which side of the range moved" is not recoverable from a digest. That
`continue` is the exact hole — a digest refresh always moves the manifest's
bytes.

The third point is the one nothing in the working tree can rewrite: the signed
`registry.snapshot.json` of the last released tag.

## 2. What landed

| File | State | Owner |
| --- | --- | --- |
| `internal/modkit/revision_baseline.go` | new (`LoadReleaseBaseline`, `ValidateRevisionsAgainstReleaseBaseline`, `movedSinceRelease`, `scanRegistryManifests`) | `ggg/system/modkit` |
| `internal/modkit/revision_baseline_selfhost_test.go` | new, `self_host: true` (the guard + the incident replay) | `ggg/system/modkit` |
| `internal/modkit/registry_build.go` | modified — both existing gates now share `scanRegistryManifests`; no behaviour change | `ggg/system/modkit` |
| `internal/modkit/registry_test.go` | modified — fixture unit tests for the new validator | `ggg/system/modkit` |
| `internal/modkit/ci_workflow_test.go` | modified — pins the `test` job's `fetch-depth: 0` | `ggg/system/modkit` |
| `internal/gggcli/selfhost_test.go` | modified — three new `inapplicableSkipSites` rows | `ggg/system/modkit` |
| `internal/modkit/testdata/redproof/inventory.txt` | modified — one `allow:` line for the new guard | `ggg/system/modkit` |
| `.github/workflows/ci.yml` | modified — `test` job checkout at `fetch-depth: 0` | `ggg/system/ci-github` |
| `AGENTS.md` | modified — the third gate, and 64 → 65 self_host payloads | `ggg/system/project-docs` |
| `registry/modules/system/modkit/module.json` | modified — two appended `files` rows | — |

No revision bumps, no `registry build`/`sign`/`sync`, no push, no tag.

## 3. Placement: `make check`, and what it costs

The guard is `TestEveryModuleChangedSinceTheLastReleaseCarriesAHigherRevision`
in the accounted suite, not a step in the release order.

**Why not the release order.** The rule needs git history, and modkit's engine
has none: `internal/gggcli/gate.go` is the only non-test code in this
repository that shells out to git, and it does so at the command tier. Wiring
`git show` into `registry build` would also make the build behave differently
inside and outside a worktree — every `/tmp` derivative `ggg registry validate`
builds, and every third-party publisher's tree, has no tags — so the rule would
be inert in most of its invocations while claiming to be a build gate. That is
the same vacuity in a new place.

**Why `make check`.** At release time the evidence is a whole cycle of commits
and the release owner has to archaeologise which edit moved which payload;
that forensic diff is exactly what the v0.26.0 integration paid, four modules
deep. In `make check` the refusal arrives in the commit that moves the bytes,
where the remedy is one line written by the person who knows what changed.

**Costs, measured:**

- one `git archive <tag> registry.snapshot.json registry/modules` (1 720 320
  bytes at v0.25.0, read as a tar stream, never unpacked) plus one SHA-256 per
  published payload still present in the tree. The guard logs it every run:
  `release baseline v0.26.0: 297 published modules compared in 525ms`.
- the `test` job's checkout moves to `fetch-depth: 0`. Pack size is 51.62 MiB
  against a 37 MB tree (`git count-objects -vH`, `git archive HEAD | wc -c`);
  the wall-clock delta on the runner is *not* measured here, and that job
  already pulls two container images. Only that job pays it — no other job runs
  the accounted suite — and `TestCITestJobRunsTheAccountedSuiteUnderRace` now
  fails if the depth is dropped.
- the social cost, stated plainly: a module's revision is now bumped in the
  commit that edits it rather than in a release-time sweep. It is per RELEASE,
  not per commit — once a module sits above its published revision, every
  further edit in that cycle passes, which the unit test pins directly.

**Deletion.** A module present in the baseline and absent from the tree passes.
The refusal's own remedy — bump the revision — names an edit to a manifest that
no longer exists, so demanding a phantom bump would block every legitimate
module removal and teach the release owner to route around the gate. Removal is
the catalog's business: the index build drops it and the lock tombstones it.

## 4. The founding incident, reproduced

`TestReleaseBaselineGateRefusesTheV0260IntegrationIncident`: baseline
**v0.25.0**, tree at **5b48bfa8** (`Give the canary suite a home that may name
an adapter` — the commit immediately before the release bump that corrected
it), materialised with `git archive` into a temp directory.

```
=== RUN   TestReleaseBaselineGateRefusesTheV0260IntegrationIncident
    revision_baseline_selfhost_test.go:164: refusal:
        bytes changed since the last released registry snapshot (v0.25.0) with no revision above the one published there: ggg/system/ci-github (published revision 6, tree revision 6; manifest bytes, .github/workflows/ci.yml), ggg/system/content-assets (published revision 43, tree revision 43; manifest bytes, content/docs/roadmap.md, content/docs/testing.md), ggg/system/mail-smtp (published revision 3, tree revision 3; manifest bytes), ggg/system/notifications-knock (published revision 1, tree revision 1; manifest bytes, internal/notifications/knock/knock.go, internal/notifications/knock/knock_test.go), ggg/system/storage-s3 (published revision 4, tree revision 4; manifest bytes, internal/storage/s3/contract_test.go), ggg/system/usage-openmeter (published revision 1, tree revision 1; manifest bytes, internal/usage/openmeter/openmeter.go, internal/usage/openmeter/openmeter_test.go), ggg/system/webhooks-svix (published revision 1, tree revision 1; manifest bytes, internal/webhooks/svix/svix.go, internal/webhooks/svix/svix_test.go). revision is the module's version of record — it feeds indexSHA and `ggg info` — so an implementation change that leaves it alone publishes a lie. The manifest-against-lock and manifest-against-disk gates cannot see this: one edit that moves a payload AND refreshes the digest recorded for it leaves both sides agreeing at the old revision. Bump revision above the published one — once per release, not once per commit (contract moves only when a consumer must change code)
```

Seven modules, each with its published and its tree revision. The parent's
four — `mail-smtp` (3), `notifications-knock` (1), `usage-openmeter` (1),
`webhooks-svix` (1) — are all named, and the digests in the parent's table are
the `manifest bytes` evidence on those four rows.

**The exact-fix revert, in the same test.** The unfixed state is the two
existing gates, run against the same tree:

```
    revision_baseline_selfhost_test.go:192: ValidateManifestRevisionsAgainstSnapshot over the same tree: (no refusal)
    revision_baseline_selfhost_test.go:192: ValidateManifestRevisions over the same tree: payload digests changed without a revision bump: ggg/system/ci-github (revision 6), ggg/system/content-assets (revision 43), ggg/system/storage-s3 (revision 4). revision is the module's version of record — it feeds indexSHA and `ggg info` — so an implementation change that leaves it alone publishes a lie. Bump revision (contract moves only when a consumer must change code)
```

Neither names any of the four. The snapshot half sees nothing at all (every
manifest had been refreshed, so every one of them took the `continue`); the
lock half sees only the three whose manifests had *not* absorbed their payloads'
new digests. The test asserts that separation, so a future change that made the
four visible to the old gates would fail the replay as "not the incident"
rather than quietly passing.

## 5. Both directions

**Red — bytes moved, revision stood still.** The seven above. And in fixture
form (`TestValidateRevisionsAgainstReleaseBaselineRefusesBytesThatMovedWithoutABump`),
the incident shape exactly: payload rewritten and the manifest's recorded
digest refreshed in the same edit, at the same revision — refused, naming the
module, both revisions and the baseline. A revision that moved *backward* while
the bytes moved is also refused: the published revision is a floor, not a
difference.

**Green — legitimately bumped.** The release that corrected the incident, over
the same real history, asserted in the same test and required to be
non-vacuous:

```
    revision_baseline_selfhost_test.go:226: v0.26.0 against baseline v0.25.0: 9 modules moved and bumped, 288 untouched and unbumped
--- PASS: TestReleaseBaselineGateRefusesTheV0260IntegrationIncident (3.30s)
```

Nine moved-and-bumped modules is the commit message's own count (`Bump the nine
modules the canary program's payload changes touch`). The fixture test adds the
per-release half: after one bump, a *second* edit at the same bumped revision
still passes — the gate never demands a bump per commit.

**Green — untouched, the false-positive risk.** The 288 in the line above are
modules whose manifest and every payload are byte-identical to v0.25.0; they
pass with no bump, and the test fails if fewer than 200 of them are in that
state, so the green can never come from an empty comparison. The fixture test
pins the same case in isolation, plus the new-module case (absent from the
baseline → nothing to compare → passes at revision 1) and the deletion case.

## 6. The shallow-history refusal

Loud, with its remedy, and never a vacuous pass. Proven in a real shallow clone
(`git clone --depth 1 --no-tags file://…`, `git rev-parse
--is-shallow-repository` → `true`), with this change's files copied in:

```
=== RUN   TestEveryModuleChangedSinceTheLastReleaseCarriesAHigherRevision
    revision_baseline_selfhost_test.go:113: [inapplicable] the release-baseline revision gate reads the last released registry.snapshot.json out of git and needs the full history with its tags; this clone is shallow or has no readable git history — run `git fetch --unshallow && git fetch --tags origin` and re-run
--- SKIP: TestEveryModuleChangedSinceTheLastReleaseCarriesAHigherRevision (0.02s)
=== RUN   TestReleaseBaselineGateRefusesTheV0260IntegrationIncident
    revision_baseline_selfhost_test.go:146: [inapplicable] the release-baseline revision gate reads the last released registry.snapshot.json out of git and needs the full history with its tags; this clone is shallow or has no readable git history — run `git fetch --unshallow && git fetch --tags origin` and re-run
--- SKIP: TestReleaseBaselineGateRefusesTheV0260IntegrationIncident (0.02s)
```

And the second refusal, after `git fetch --unshallow --no-tags` (deep history,
zero tags):

```
=== RUN   TestEveryModuleChangedSinceTheLastReleaseCarriesAHigherRevision
    revision_baseline_selfhost_test.go:113: [inapplicable] no release tag is reachable from HEAD, so there is no published snapshot to measure this tree against; run `git fetch --tags origin` (or tag the first release) and re-run
--- SKIP: TestEveryModuleChangedSinceTheLastReleaseCarriesAHigherRevision (0.04s)
```

Both carry `[inapplicable]` with a reason, so the accounted gate counts them
apart instead of refusing the run; all three skip sites are declared in
`inapplicableSkipSites`, which
`TestEveryInapplicableSkipSiteIsDeclared` enforces both ways. The guard also
refuses a baseline that resolved fewer than 200 published modules, so a load
that collapses cannot report a clean line either.

## 7. Red proof: an allowance, not a patch

The guard's red is **not patch-shaped**. `redproofMaterialiseScratch` strips
`.git` and gives the scratch copy a fresh identity, so the copy has no release
tag; a planted violation there meets the `[inapplicable]` skip, which the
runner would classify as a masquerade rather than a proof. The inventory line
says exactly that — the same shape as the existing
`TestEveryDeclaredContainerIsExecutable` allowance — and names this report for
the hand-driven reds (§4, §5). The first of those reds is wired in permanently
as `TestReleaseBaselineGateRefusesTheV0260IntegrationIncident`, which is the
closest a history-shaped guard gets to a corpus patch: it is a real violation,
from real history, that the guard must keep catching.

## 8. Verification

```
$ go build ./...
$ go vet ./...
(both silent)

$ go test ./internal/modkit -run 'TestValidateRevisionsAgainstReleaseBaseline|TestLoadReleaseBaseline|TestValidateManifestRevisions' -count=1 -v
--- PASS: TestValidateManifestRevisionsRefusesChangedPayloadsAtTheSameRevision (0.01s)
--- PASS: TestValidateRevisionsAgainstReleaseBaselineRefusesBytesThatMovedWithoutABump (0.01s)
--- PASS: TestLoadReleaseBaselineRefusesATreeThatDisagreesWithItsSnapshot (0.00s)
ok  	github.com/gogogadget/gogogadget/internal/modkit	0.435s

$ go test ./internal/modkit -run 'TestReleaseBaselineGateRefusesTheV0260IntegrationIncident' -count=1
ok  	github.com/gogogadget/gogogadget/internal/modkit	3.767s

$ go test ./internal/modkit -run 'TestAgents|TestEveryTrackedSourceFileHasAnOwner|TestPublished|TestEveryVendored|TestEveryInapplicable' -count=1
ok  	github.com/gogogadget/gogogadget/internal/modkit	2.488s

$ go test ./internal/modkit -run 'TestCITestJobRunsTheAccountedSuiteUnderRace|TestCIExercisesEveryClosureFamilyForReal|TestCIWorkflowCancelsSupersededRuns|TestCICoverJob' -count=1
ok  	github.com/gogogadget/gogogadget/internal/modkit	0.642s

$ go test ./internal/gggcli -run 'TestEveryInapplicableSkipSiteIsDeclared' -count=1
ok  	github.com/gogogadget/gogogadget/internal/gggcli	0.398s

$ go test ./internal/modkit -run 'TestRedProofGate' -count=1 -timeout 45m
ok  	github.com/gogogadget/gogogadget/internal/modkit	166.382s
```

Full-package run, for completeness:

```
$ GGG_REDPROOF=off go test ./internal/modkit -count=1
--- FAIL: TestEveryModuleChangedSinceTheLastReleaseCarriesAHigherRevision (0.97s)
--- FAIL: TestCoreRepositoryInstallsEverySelfHostPayload (0.14s)
    selfhost_test.go:119: self_host payload internal/modkit/revision_baseline_selfhost_test.go of ggg/system/modkit is declared but not installed here; the core gate no longer runs it
--- FAIL: TestEveryShippedProfileResolvesIntoACoherentProject (0.67s)   [+ 4 subtests]
--- FAIL: TestShippedProfileGateCatchesABrokenProfile (0.49s)           [+ 2 subtests]
--- FAIL: TestTheDocumentedProfileTableMatchesWhatTheProfilesResolveTo (0.21s)
--- FAIL: TestShippedWalkthroughProfilesIncludeTheSeedLoader (0.23s)
        	            	resolve registry ggg at : registry snapshot payload "registry/modules/system/modkit/module.json" digest mismatch
```

Every one of those six is release-order staleness and nothing else: the five
profile/plan failures are the single `registry snapshot payload
"registry/modules/system/modkit/module.json" digest mismatch` (the committed
`registry.snapshot.json` still pins the pre-change manifest), and
`TestCoreRepositoryInstallsEverySelfHostPayload` is the lock not yet listing
the new self_host payload. `registry build && sign && sync` clears both.

`make check` and `registry validate` are the parent's integration step. The
seventh failure — the new guard firing on this change itself, §9 — is the gate
working, not staleness.

## 9. Implied revision bumps — NOT applied

The new guard, run against the working tree it ships in (baseline v0.26.0,
which is HEAD):

```
    revision_baseline_selfhost_test.go:124: release baseline v0.26.0: 297 published modules compared in 525ms
    revision_baseline_selfhost_test.go:126: bytes changed since the last released registry snapshot (v0.26.0) with no revision above the one published there: ggg/system/ci-github (published revision 7, tree revision 7; .github/workflows/ci.yml), ggg/system/modkit (published revision 87, tree revision 87; manifest bytes, internal/gggcli/selfhost_test.go, internal/modkit/ci_workflow_test.go, internal/modkit/registry_build.go, and 2 more), ggg/system/project-docs (published revision 21, tree revision 21; AGENTS.md)
--- FAIL: TestEveryModuleChangedSinceTheLastReleaseCarriesAHigherRevision (0.67s)
```

So the release order needs:

| Module | Published at v0.26.0 | Needs |
| --- | --- | --- |
| `ggg/system/ci-github` | 7 | ≥ 8 |
| `ggg/system/modkit` | 87 | ≥ 88 |
| `ggg/system/project-docs` | 21 | ≥ 22 |

`registry build` will also want its digest refresh for the same payloads (the
manifest rows for the two new files carry hand-computed SHA-256s, so they are
already correct; the edited payloads are not). The guard's own red is the first
time this convention has been enforced against the commit that breaks it, and
this change is its first customer.
