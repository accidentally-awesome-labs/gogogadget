# task-bh — verification installs: making the canonical lock reproducible, and `ggg add` honest

## 1. What I read first, and what the sites actually said

**The refusal.** `internal/modkit/resolve.go:359` (pre-change):

```go
return selectedGraph{}, fmt.Errorf("explicit adapter %q is not selected by provider choices; use ggg provider set", explicitID)
```

It fires only for ids in `Project.Modules`, after the provider fixpoint has computed
`chosenAdapters`.

**The resolver path.** `resolveSelectedGraph` (`resolve.go:148`) seeds `selected` from
`Project.Modules` — a module id enters with reason `explicit`, a profile id expands to its members
with reason `profile` — and then, at lines 188-194:

```go
// Profiles publish candidate adapters for discoverability, but adapter
// activation is exclusively controlled by Project.Providers. Treating
// every candidate as a profile member makes an unselected adapter an
// explicit module and prevents registry closure validation.
if module.Runtime.System != nil && module.Runtime.System.Adapter != nil {
    continue
}
```

So a profile-member adapter is **skipped**. Adapters enter only through the provider fixpoint
(reason `provider`), plus `Project.Deployment` (reason `deployment`) and `requires` closure (reason
`dependency`).

**Why `ggg add` returned `ok: true` having done nothing.** `projectAfterAdd`
(`reconcile.go:82-92`, pre-change):

```go
supplied := false
for _, selectedID := range result.Modules {
    profile, ok := profiles[selectedID]
    if ok && containsString(profile.Members, id) {
        supplied = true
        break
    }
}
if !supplied {
    result.Modules = append(result.Modules, id)
}
```

`ggg/system/feature-flags-flagsmith` **is** a member of `ggg/profile/full`, and this project selects
`ggg/profile/full`. So `supplied` was true, nothing was appended, intent was unchanged — and the
resolver then skipped the adapter under the rule quoted above. Two skips in series produced one exit
0. Measured at HEAD (`2181c197`), in a throwaway copy:

```
$ ggg add ggg/system/feature-flags-flagsmith --json
{"ok":true,"exit":0,"conflicts":[],"diagnostics":[],"changes":28}
LOCK UNCHANGED
(no flagsmith row in the lock; no flagsmith in the intent)
```

The 28 "changes" were all `kind:"unchanged"` rows. `sync` refused the same intent by name.

**The precedent.** `ValidatePayloadAdapterImports` (`cli_scan.go:193-216`) states it exactly:

> `self_host: true` payloads are outside this rule's DOMAIN rather than an exception to it … A
> self_host payload is never installed into a derivative at all … It is installed into exactly one
> project — the publishing repository — whose gogogadget.json sits beside it in the same commit and
> whose lock is verified by the same gate, so its dependency on those selections is declared, fixed,
> and checked rather than undeclarable. That is what makes the carve-out sound, and it is narrow: it
> turns on the payload's own self_host flag and nothing else.
>
> What needs it: internal/canary's managed-target live canary suite must import every managed
> adapter, because the generated per-slot accessors expose only the SELECTED adapter per slot and
> the suite's entire purpose is to exercise the unselected ones too.

## 2. The rule as implemented

`Project.VerificationInstalls` — JSON `verification_installs`, optional (`omitempty`, like `ports`,
so a project file at rest keeps loading), sorted, beside `modules`/`exclude`/`providers`.

Enforcement, all of it:

| Rule | Site | Refusal |
|---|---|---|
| only the **publisher** may declare one, per entry, against that registry's `canonical_module` | `ValidateVerificationInstallPublisher`, called from `Plan` before the resolver and before any write | names the project's module path, the entry, and the publishing registry's canonical module |
| the entry must be an **adapter** | `resolveSelectedGraph` | `is not an adapter; only a module contributing runtime.system.adapter can be installed for verification` |
| it must publish a **`managed`** service target | `resolveSelectedGraph` | `publishes no managed service target; a development or self-hosted target is reachable through ggg provider set …` |
| it must not already be **selected** | `resolveSelectedGraph`, after the fixpoint | `already selected by provider choices; a verification install installs an adapter NOTHING selects …` |
| it must be in the **catalog** | both | `is not present in the catalog` |
| every entry needs a **live-canary row** | `TestEveryCanaryRowIsInstalledAndEveryVerificationInstallIsProbed` (`internal/canary`) | `gogogadget.json declares these verification installs and no liveCanaryProviders row probes them: …` |
| every **canary row** must be selected or verification-installed | same guard, other direction | `these liveCanaryProviders rows drive adapters this repository neither selects nor installs for verification: …` |
| `ggg remove` of one refuses | `planRemove`, symmetric with the selected-adapter refusal | `delete its entry from gogogadget.json verification_installs first, and delete its live-canary row in internal/canary in the same commit` |
| a deselected adapter now verification-installed is **not** retired | `retiredAdapters` | — (one plan would otherwise delete and install the same module) |

The publisher test reuses `InstallsSelfHostPayloads(modulePath, catalog.ModuleCanonical[id])` — the
same discriminator `ManifestFile.SelfHost` turns on — deliberately: the soundness argument is
identical, so the test is identical.

A verification install carries lock reason `verification` (`modkit.VerificationReason`). Nothing
selects it, so the generated boot constructs it in no environment, its `required` environment keys
are enforced nowhere, and `providerActive` gating leaves every executable contribution unregistered.
None of the five declares a migration, a route, a job or a janitor — only `runtime.system`.

## 3. The five zombies' disposition

They were **not installs**. Each was a `reason:"removed"` tombstone with `files: []`,
`migrations: []` and a gutted manifest — the shape `planRetirement` leaves when an adapter stops
being selected. `reconcilePlannedState` carried every tombstone forward verbatim, so they persisted
as lock members that explained nothing. Meanwhile their payload *sources* sat in `internal/` (source
== target in the canonical repo) owned by no lock row, which is why the canary compiled at all.

| module | before | after | mechanism |
|---|---|---|---|
| `ggg/system/feature-flags-flagsmith` | absent from the lock entirely | installed, reason `verification`, 2 files | new `verification_installs` entry |
| `ggg/system/notifications-knock` | tombstone, 0 files | installed, reason `verification`, 2 files | tombstone dropped by re-selection (`reconcile.go` already skips a tombstone whose module is selected again) |
| `ggg/system/search-typesense` | tombstone, 0 files | installed, reason `verification`, 3 files | same |
| `ggg/system/usage-openmeter` | tombstone, 0 files | installed, reason `verification`, 2 files | same |
| `ggg/system/webhooks-svix` | tombstone, 0 files | installed, reason `verification`, 2 files | same |
| `ggg/system/feature-flags-launchdarkly` | tombstone, 0 files | **absent from the lock** | tombstone **prune** |

**The launchdarkly mechanism, named: a prune, not the removal path and not plain non-resolution.**
Plain non-resolution was already true — nothing resolved it, which is exactly why it could not be
reached by any command — and the removal path had already run (that is what produced the tombstone).
What was missing is that a tombstone exists to retain an immutable migration ledger so a re-add
reuses the numbers. A tombstone with an **empty ledger** for a module **no configured registry
publishes** retains nothing and can never be re-added to reuse anything: it is a ledger row with no
ledger. `reconcilePlannedState` now drops exactly that intersection. Both halves are required:

- an empty-ledger tombstone for a module still in the catalog **stays**, because `ggg remove` of a
  migration-free module must leave the removal on the record and a re-add must see it;
- a non-empty ledger **stays** whatever the catalog says, because those migration files are on disk
  and their numbers must never be reused.

The invariant is asserted where the arithmetic depends on it, in the reproducibility guard.

## 4. The `ggg add` decision

**`add` refuses, and both verbs render the same bytes.** Your reading was right, and the reason is
not symmetry for its own sake: an adapter is a per-environment provider *selection*, so "install
this adapter" is not an intent `modules` can express. `add` has no honest way to perform it — it
would have to invent a provider choice the operator did not make, in an environment it would have to
pick. So it names the two things that *can* express it.

One function, `modkit.UnselectedAdapterRefusal(id, slot)`, is the only text either verb produces.
Measured in the after copy:

```
$ ggg add ggg/system/mail-smtp --json     →  ok:false, exit 3
adapter "ggg/system/mail-smtp" is not selected by provider choices; an adapter is installed only
where some environment selects it. Select it with `ggg provider set --provider
ggg/mail:ENVIRONMENT=ggg/system/mail-smtp@TARGET`, or — only in the repository that publishes it —
add it to gogogadget.json `verification_installs`, which installs it for the live canary in
internal/canary and selects it for nothing

$ (mail-smtp placed in gogogadget.json modules) ggg sync --offline --json  →  ok:false, exit 3
adapter "ggg/system/mail-smtp" is not selected by provider choices; an adapter is installed only
where some environment selects it. Select it with `ggg provider set --provider
ggg/mail:ENVIRONMENT=ggg/system/mail-smtp@TARGET`, or — only in the repository that publishes it —
add it to gogogadget.json `verification_installs`, which installs it for the live canary in
internal/canary and selects it for nothing
```

Byte-identical. The word "explicit" left the message so both call sites could share it verbatim.

Two layers, on purpose. `projectAfterAdd` renders the refusal for the adapter case — the site of the
lie. `Plan` then carries a general backstop for `OpAdd`: every operand that is a catalog *module*
must appear in the resolved graph, or the plan refuses rather than reporting success. That closes
every other way a future skip could make `add` silent:

```go
"add resolved but would install nothing for %v: the operands are in the catalog and absent from the
resolved graph, so reporting success here would be a lie. Name the reason in the refusal that
applies to them rather than widening this backstop"
```

Covered by `TestAddRefusesAnAdapterNoEnvironmentSelects` (`internal/modkit/task2_followup_test.go`),
whose fixture is the shape that got away — the operand is a profile member, so the old
"already supplied" branch returned an unchanged document. It asserts the refusal happens **and**
that it is the shared text, with two controls: the same operand is accepted when a provider choice
names it, and is a genuine no-op when `verification_installs` declares it.

## 5. Reproducibility — the numbers

Both measured by running `registry build` + `sync --offline` in throwaway copies at `/tmp`, so the
parent's dirty lock, generated set, and `registry.snapshot.*` were never touched.

**Before** (`git archive HEAD`, then `sync --offline`, exit 0, lock byte-stable):

```
{ "total": 293, "installed": 288, "tombstones": 5 }
reasons: 1 deployment, 257 profile, 30 provider, 5 removed
removed: feature-flags-launchdarkly, notifications-knock, search-typesense, usage-openmeter, webhooks-svix
```

**After** (this change, `registry build` exit 0, `sync --offline` ok:true exit 0):

```
{ "total": 293, "installed": 293, "verification": 5, "tombstones": 0 }
reasons: 1 deployment, 257 profile, 30 provider, 5 verification
verification: feature-flags-flagsmith (2 files), notifications-knock (2), search-typesense (3),
              usage-openmeter (2), webhooks-svix (2)
launchdarkly: absent from the lock
```

The total is 293 both times and the membership is not: **−1** launchdarkly, **+1** flagsmith, and
four rows moved from 0-file tombstones to real installs with `state: clean` file rows.

**The identity.** A fresh `full` genesis resolves 288 modules — I ran the real thing,
`ggg new /tmp/ggg-vi/genesis --answers … --non-interactive`, and got `{total: 288, installed: 288}`.
So:

```
installed(core lock)  ==  closure(ggg/profile/full)  +  verification_installs
        293           ==            288              +           5
```

and as a **set**, not a count:

```
$ diff <(core lock, reason != verification|removed) <(fresh genesis, reason != removed)
SETS IDENTICAL: 288 == 288
```

**Asserted**, the way the repo already asserts closure counts (`profile_genesis_test.go` checks each
profile's lock against the count its own description advertises):
`TestTheCommittedLockDecomposesIntoTheFullProfileClosureAndItsVerificationInstalls`, a new self_host
guard in `internal/modkit/lock_reproducibility_selfhost_test.go`. It

1. requires the lock's `verification` rows to equal `gogogadget.json`'s `verification_installs`
   exactly;
2. re-derives the fresh genesis **in process** — profile `ggg/profile/full` as the only explicit
   module, `exclude` computed the way `previewNew` computes it, and the project's own provider and
   deployment choices — and requires set equality with the lock's non-verification rows, quoting
   both counts in the failure;
3. requires `len(lock.Modules) == len(fresh) + len(verification_installs) + len(tombstones)`;
4. requires no tombstone to have an empty ledger and an unpublished module.

Providers and deployment come from the project rather than the profile's defaults because both are
project decisions, not profile members: `ggg/profile/full`'s `default_deployment` is
`ggg/system/deploy-docker` and this repository self-hosts on `ggg/system/deploy-fly`. Taking the
profile default reports a one-module difference that is a correct choice — I drove exactly that and
the guard said `- ggg/system/deploy-docker / + ggg/system/deploy-fly`.

**One residual difference, named rather than papered over.** The committed `exclude` carries three
entries; a fresh genesis derives one (`ggg/system/deploy-docker`). `ggg/component/table-empty` and
`ggg/element/divider` are **not members of `ggg/profile/full`** (286 members, neither among them) and
nothing reaches them through `requires`, so both exclude entries are inert — they exclude modules the
profile never offered. That is why the two sets match despite the different exclude lists, and it is
why the guard derives `exclude` instead of reading the committed one: the committed list cannot
smuggle a member in or out of the comparison. The two dead entries are hand-owned intent I did not
touch; removing them is a one-line change for whoever owns that call.

`AGENTS.md`'s catalog line moved from `288 selected here` to `293 installed here`, and
`TestAgentsCatalogCountsMatchTheRegistryAndTheLock` moved with it — "selected" and "installed" now
name different sets and the document has to use the one it means.

## 6. Driven red, all quoted

**A derivative declaring a verification install is refused** — `TestVerificationInstallRefusals`,
and the text over the real catalog:

```
gogogadget.json declares verification install "ggg/system/feature-flags-flagsmith" and this project
does not publish it: its go.mod module path is "example.com/acme/app", while
ggg/system/feature-flags-flagsmith is published by a registry whose canonical_module is
"github.com/gogogadget/gogogadget". A verification install is sound only in the publishing
repository, where the adapter, the intent that declares it and the canary that probes it sit in one
commit and are checked by one gate. In a derivative it is a dependency on an adapter no environment
selects, which no `requires` can express. Remove the entry, or select the adapter with `ggg provider set`
```

The same test drives the other narrowings: a self-hosted adapter (`publishes no managed service
target`), a seam (`is not an adapter`), an already-selected adapter (`already selected by provider
choices`), an absent id (`not present in the catalog`), plus the green control that the publishing
repository's own entry resolves with reason `verification`.

**A verification install with no canary row is refused** — plant: flagsmith's row `Module` renamed:

```
--- FAIL: TestEveryCanaryRowIsInstalledAndEveryVerificationInstallIsProbed
  gogogadget.json declares these verification installs and no liveCanaryProviders row probes them:
  [ggg/system/feature-flags-flagsmith].
  The rule: A managed target is admissible only if the seam can be satisfied through a documented
  HTTP contract in which the PROVIDER performs the decision — no vendored SDK, no reimplementation
  of vendor semantics.
  A verification install is the ONE way this repository installs an adapter nothing selects, and its
  entire justification is that the canary must import it. Without a row it installs an adapter for nothing.
  Remedy: write the probe as a liveCanaryProviders row, or delete the entry from verification_installs.
```

**A canary row for an adapter neither selected nor verification-installed is refused** — corpus
patch `own-canary-row-uninstalled` (svix dropped from `verification_installs`, row and import kept —
the exact state this repository shipped in):

```
--- FAIL: TestEveryCanaryRowIsInstalledAndEveryVerificationInstallIsProbed
  these liveCanaryProviders rows drive adapters this repository neither selects nor installs for
  verification: [ggg/system/webhooks-svix].
  A row imports the adapter's package directly — that is the whole reason the self_host carve-out in
  ValidatePayloadAdapterImports exists — so the row compiles against source no lock row owns:
  invisible to install, to update and to removal, and reproducible by no `ggg sync`.
  Remedy: add each module to gogogadget.json `verification_installs` (installed for verification,
  selected by nothing), select it with `ggg provider set`, or delete the row.
```

**`ggg add` of an unselected adapter refuses instead of no-opping** — before/after quoted in §4,
and `TestAddRefusesAnAdapterNoEnvironmentSelects` fails if `add` ever again returns a document for
that operand.

Both directions are also driven on a synthetic population by
`TestLiveCanaryInstallClassificationRefusesBothDirections`, because the real repository can carry
neither defect once the gate passes and a refusal nobody can observe is a refusal nobody maintains.

**Corpus, patch-shaped as the house rules require.** Two patches, because one patch names one
package and the rule has two halves in two packages:

- `own-canary-row-uninstalled.patch` → `TestEveryCanaryRowIsInstalledAndEveryVerificationInstallIsProbed` (`./internal/canary`)
- `own-lock-unreproducible.patch` → `TestTheCommittedLockDecomposesIntoTheFullProfileClosureAndItsVerificationInstalls` (`./internal/modkit`)

Both plant in `gogogadget.json` rather than in the suite or the lock: the guards' populations are the
committed intent read through `modkit.ParseProject`, so the proof tests the intent read as well as
the refusal, and the lock is generated — hand-editing it is the thing `AGENTS.md` forbids.
`internal/modkit/testdata/redproof/inventory.txt` regenerated via
`GGG_REDPROOF_UPDATE_INVENTORY=1`; `AGENTS.md`'s self_host paragraph moved 65 → 68 payloads and
30 → 32 patches, with both new patch ids named so
`TestAgentsSelfHostInventoryMatchesTheManifests` can reach them.

## 7. One defect the change surfaced, and its fix

`sync` refused on the first attempt with something I had not gone looking for:

```
internal/flags/flagsmith/flagsmith_test.go is owned by ggg/system/feature-flags-flagsmith and
imports adapter package internal/cache/memory owned by ggg/system/cache-memory; an adapter is a
per-environment provider selection, which no `requires` can express, so a payload must reach it
through the generated wiring or use the seam's own test double
```

Real, and invisible for a release precisely because nothing installed the module. The remedy is a
double the flagsmith module owns, in `flagsmith_test.go`, which the rule exempts. I did **not** add
`cache.NewMemory()` to the `ggg/system/cache` seam — `search.NewMemory` and `realtime.NewMemory`
exist because their adapters are Postgres and have nothing in-memory to duplicate, whereas
`internal/cache/memory` **is** the in-memory cache, so a seam double would be duplication two
modules must keep in sync (and would cost a new package test plus the removal of `internal/cache`'s
stated no-tests allowance). The double honours the TTL rather than ignoring it, because
`decisionTTL` is what this adapter's staleness argument rests on and a double that dropped it would
make `TestEnabledCachesOneProviderCallForEveryKeyOfOneOrganisation` pass for the wrong reason.

## 8. Revision bumps applied, in this commit

| module | payloads I changed | revision |
|---|---|---|
| `ggg/system/modkit` | `internal/modkit/{model,codec,validate,resolve,plan,reconcile,remove}.go`, `internal/modkit/agents_counts_selfhost_test.go`, `internal/modkit/task2_followup_test.go`, new `internal/modkit/lock_reproducibility_selfhost_test.go`, new `testdata/redproof/own-canary-row-uninstalled.patch`, new `testdata/redproof/own-lock-unreproducible.patch`, `testdata/redproof/inventory.txt`, `internal/canary/live_canary_selfhost_test.go` | **89 → 90** |
| `ggg/system/feature-flags-flagsmith` | `internal/flags/flagsmith/flagsmith_test.go` | **1 → 2** |
| `ggg/system/project-docs` | `AGENTS.md` | **23 → 24** |

`registry/schema/project.schema.json` gained `verification_installs` (array of
`<ns>/system/<name>`, `uniqueItems`, optional) and is registry-tree data owned by no module, so it
carries no revision of its own.

## 9. Verification, quoted

```
$ go build ./...                                                        (no output)
$ go vet ./internal/modkit ./internal/canary ./internal/flags/... ./internal/gggcli
BUILD-VET-OK
$ gofmt -l internal/modkit/ internal/canary/ internal/flags/flagsmith/  (no output)

# in the throwaway copy, after registry build + sync --offline (both exit 0):
$ go test ./internal/modkit ./internal/canary -run '<21 guards, listed below>'
ok  github.com/gogogadget/gogogadget/internal/modkit   7.866s
ok  github.com/gogogadget/gogogadget/internal/canary   0.399s

$ go test ./internal/flags/...
ok  github.com/gogogadget/gogogadget/internal/flags            3.130s
ok  github.com/gogogadget/gogogadget/internal/flags/flagsmith  0.211s
ok  github.com/gogogadget/gogogadget/internal/flags/postgres   0.161s

$ go test ./internal/modkit -run 'TestRedProofGate/(coverage|own-canary-row-uninstalled|own-lock-unreproducible)'
--- PASS: TestRedProofGate/coverage
--- PASS: TestRedProofGate/own-canary-row-uninstalled
--- PASS: TestRedProofGate/own-lock-unreproducible
```

The 21: `TestVerificationInstallRefusals`, `TestAddRefusesAnAdapterNoEnvironmentSelects`,
`TestProviderSelectionAndClaimRefusals`, `TestPublishedSchemasMatchModels`,
`TestAgentsManifestKeysEqualTheModelAndTheSchema`, `TestPublishedSchemaInstancesValidate`,
`TestEveryShippedProfileResolvesIntoACoherentProject`,
`TestAgentsSelfHostInventoryMatchesTheManifests`,
`TestAgentsCatalogCountsMatchTheRegistryAndTheLock`,
`TestSelfHostPayloadsInstallOnlyIntoThePublishingRepository`,
`TestValidatePayloadAdapterImports`, `TestTheCommittedLockDecomposes…`,
`TestEveryTrackedSourceFileHasAnOwner`, `TestThePublishedCatalogClaimsEveryTargetExactlyOnce`,
`TestPublishedRegistryIsConsistent`, `TestEveryCanaryRowIsInstalled…`,
`TestLiveCanaryInstallClassificationRefusesBothDirections`,
`TestEveryManagedTargetHasALiveCanaryOrAnAdmissibleExcuse`,
`TestEveryFileInThePublishedRegistryTreesHasADeclaringOwner`.

## 10. What is left for the release order

Everything I committed is hand-owned. `gogogadget.lock.json`, the `*_registry_gen.*` set,
`registry.snapshot.json` and `registry.snapshot.sig` are untouched by me and stale against this
commit by construction. Until `registry build` + `sign` + `sync --offline` run in-tree,
`TestTheCommittedLockDecomposes…` and `TestAgentsCatalogCountsMatchTheRegistryAndTheLock` are red
against the committed lock (288 live rows vs the 293 the document now states), and any test that
resolves the registry reports
`registry snapshot payload "registry/modules/system/feature-flags-flagsmith/module.json" digest
mismatch`. All of them pass in the rebuilt copy, quoted above. `make check`, `make e2e`,
`make visual`, `make smoke`, `make docker-build` and the two `ggg registry validate` jobs are the
parent's.
