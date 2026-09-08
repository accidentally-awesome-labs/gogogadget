# Task AP — AGENTS.md truth gate

23 tests in five packages now fail the build when the mechanically-checkable subset of
`AGENTS.md` drifts. Every list-shaped check is bidirectional. Each was demonstrated red on an
injected drift and green at HEAD.

## The most interesting result: the four-agent audit left three defects behind

Set equality found what one-way reading could not. All three were discovered by the *reverse*
direction of a check — the document naming something the code does not have, or the code having
something the document does not name.

**1. Plane 2 named `.gitignore` as project-owned. It is a `ggg/system/project-base` payload.**
`AGENTS.md` listed six project-plane paths; the derived residue (tracked, not generated, not
catalog, declared by no manifest) has five. `.gitignore` is declared at
`registry/modules/system/project-base/module.json`, so it is plane 4 — ordinary editable source
with one module owner. The audit's replacement wording carried the error across unchanged because
it copied the `projectOwned` map in `ownership_selfhost_test.go`, and that map's `.gitignore`
entry is itself dead: the `owned[path]` arm matches first, so the exemption has been unreachable
since project-base adopted the file. Fixed in both places, and `AGENTS.md` now says so explicitly
so the next reader is not surprised.

**2. Plane 3 over-claimed twice.** It said the catalog plane is "written by `ggg registry build`/
`sign`" and enumerated `registry.json`, the snapshot, the signature and "the `registry/*.json`
indexes" — 9 paths. `registryFormatOwnedPaths` names 14 present in the tree: the missing five are
`registry/schema/{registry,module,project,lock,snapshot}.schema.json`. Those are hand-authored
JSON Schema contracts that no manifest declares and no build writes, and `registry.json` is
hand-authored too. So the plane had both the wrong membership and the wrong provenance. Plane 3 is
now the format-owned set, with the authored/generated split stated.

**3. Two in-code copies of the middleware chain were stale.** `internal/web/middleware.go:24` and
the `Handler()` doc comment in `internal/web/server.go` both listed 12 middlewares; the chain
assembles 14. Both omitted the provider-environment wrapper and `telemetry.HTTP` — the exact
omission the audit corrected in `AGENTS.md` and nowhere else. `content/docs/architecture.md`'s
tree copy was already correct. Both comments fixed, and
`TestEveryChainCommentInThisPackageAgreesWithTheAssembledChain` now keeps any in-package restatement
of the chain equal to the assembled one, so a third stale copy cannot appear.

A fourth, softer finding: **the task playbook named 10 of the 18 recipe headings** on
`/docs/extending`. The eight it never mentioned — annual pricing, search on a resource, recurring
work, OAuth provider, admin page, docs page, export, theme/rebrand — plus *B2C mode* were
invisible to an agent reading only `AGENTS.md`, which is how a recipe gets reinvented. The playbook
now names all 19 headings after the data-loss rules, and the check is set equality in both
directions.

## What landed

| Check | Test | Package |
|---|---|---|
| Plane 1, generated | `TestAgentsGeneratedPlaneEqualsTheGeneratedPredicate` | `internal/modkit` |
| Plane 2, project | `TestAgentsProjectPlaneEqualsTheUnownedResidue` | `internal/modkit` |
| Plane 3, catalog | `TestAgentsCatalogPlaneEqualsTheFormatOwnedPaths` | `internal/modkit` |
| Plane 4, its guard | `TestAgentsSourcePlaneNamesItsGuard` | `internal/modkit` |
| 23 manifest keys | `TestAgentsManifestKeysEqualTheModelAndTheSchema` | `internal/modkit` |
| 18 `runtime.*` keys | `TestAgentsRuntimeKeysEqualTheModelAndTheSchema` | `internal/modkit` |
| 16 claims families | `TestAgentsClaimFamiliesEqualTheModelAndTheSchema` | `internal/modkit` |
| 4 dependency keys | `TestAgentsDependencyKeysEqualTheModelAndTheSchema` | `internal/modkit` |
| 5 exclusivity claims | `TestAgentsRepoMapExclusivityClaimsHold` | `internal/modkit` |
| node under `e2e/` | `TestAgentsRepoMapConfinesNodeToTheE2ETree` | `internal/modkit` |
| 36 package bullets | `TestAgentsRepoMapNamesEveryInternalPackage` | `internal/modkit` |
| `notify`/`notifications`, `db`/`database` | `TestAgentsRepoMapDisambiguatesTheNearCollisions` | `internal/modkit` |
| repo-map paths exist | `TestAgentsRepoMapPathsExist` | `internal/modkit` |
| 297 published / 288 selected | `TestAgentsCatalogCountsMatchTheRegistryAndTheLock` | `internal/modkit` |
| 29 `self_host` payloads + owners | `TestAgentsSelfHostInventoryMatchesTheManifests` | `internal/modkit` |
| exit codes 0-5 | `TestAgentsExitCodeTableMatchesTheDeclaredCodes` | `internal/gggcli` |
| 19 playbook recipes | `TestAgentsPlaybookRecipesResolveToExtendingHeadings` | `internal/gggcli` |
| 13-step middleware chain | `TestAgentsMiddlewareOrderMatchesTheAssembledChain` | `internal/web` |
| app / admin / `/api` chains | `TestAgentsGroupChainsMatchTheGuardSequences` | `internal/web` |
| in-code chain comments | `TestEveryChainCommentInThisPackageAgreesWithTheAssembledChain` | `internal/web` |
| 7 design prohibitions + fixtures | `TestAgentsDesignRulesAreDocumentedAndEnforced` | `internal/web/templates` |
| `hx-confirm` scope + escape hatch | `TestAgentsHXConfirmScopeMatchesTheGuard` | `internal/web/templates` |
| 174 signatures / 175 renderers | `TestAgentsRendererCountsMatchTheReferenceRegistry` | `internal/web/templates/ui` |

Every file is a `self_host` payload of the module that owns the subject — `ggg/system/modkit`,
`ggg/system/server`, `ggg/element/ui-core` — so no derivative receives an assertion about this
repository's tree. Each file's header names the `AGENTS.md` line range it reads.

### Derivation, not restatement

- **Middleware order** is parsed out of `Handler()`, `appChain()`, `adminChain()` and the `apiWrap`
  literal with `go/ast`, then reversed (the chain assembles inside-out). No golden list exists in
  the test; a golden list in a test is the same artefact as a golden list in a document. The one
  inline `http.HandlerFunc` wrapper has no name to quote, so at that index the document must carry
  a prose label, and the wrapper's identity is pinned separately by asserting its body still calls
  `templates.WithProviderEnvironment` and `templates.WithConfigLookup`.
- **Planes** are derived from what the tree, the lock and the manifests *do*, in the sweep's own
  precedence order. Reading only the lock reported sixteen adapter sources as project-owned
  (published-but-unselected modules have payloads in the tree with an owner and no lock row), so
  both the lock and the catalog are consulted.
- **Declaration keys** are held equal three ways — document, Go model, published JSON Schema. The
  model↔schema leg already existed for three of the four types (`TestPublishedSchemasMatchModels`
  walks `Manifest`, `RuntimeContributions` and `NamespaceClaims`); what is new is the DOCUMENT leg,
  plus `Dependencies`, which is absent from that test's `modelTypes` list. Two ways would not have
  been enough anyway: the older gate validated *instances* against the schema, which only proves
  the schema is permissive enough, never that it is strict enough.
- **Design prohibitions** share one decision function with the tree scan. `ruleMatches` was
  extracted from `TestDesignSystemLayering` so the fixtures exercise the same code the guard runs;
  each of the seven has a fixture that its own rule rejects and no other rule touches, plus a clean
  fixture no rule may reject.

## Drift demonstrations — every check red, with the failure text

Each drift was applied, the owning test run, and the file restored byte-for-byte.

**`compose.yaml` removed from the generated list** (the defect that motivated the slice):
```
1 registry-owned path(s) on disk are named by no form in AGENTS.md: [compose.yaml].
Add each one to the list after "and sweeps:" in AGENTS.md plane 1 — an agent reading the
document concludes an unlisted generated file is ordinary editable source and hand-edits it.
```

**A field added to `Manifest` without documenting it:**
```
manifest keys: the Go model declares [support_tier], and AGENTS.md names none of them.
Add each key to the manifest keys list in the AGENTS.md declaration paragraph — a field nobody
documents is a declaration an agent never writes.
manifest keys: the Go model declares [support_tier] and registry/schema/module.schema.json does not.
Add each property to the published schema; the schema IS the external extension contract.
manifest keys: AGENTS.md names 23 keys, the Go model declares 24
```

**Two middleware reordered in `server.go`:**
```
AGENTS.md's middleware bullet: middleware position 9 says "maintenanceMode", Handler() assembles "rateLimit".
  stated:    [maxBytes provider-environment/config-lookup telemetry.HTTP recover routeBodyLimit requestID accessLog i18n.Detect maintenanceMode rateLimit secureHeaders sessionLoad csrf]
  assembled: [maxBytes «anonymous» telemetry.HTTP recover routeBodyLimit requestID accessLog i18n.Detect rateLimit maintenanceMode secureHeaders sessionLoad csrf]
The order is load-bearing; correct whichever one is wrong.
```

**A documented count changed** (`288 selected` → `240`, the audit's actual finding):
```
AGENTS.md says 240 selected here; the lock records 288 non-tombstone rows (of 293).
Update the `catalog` line in "## The loop".
```

The remaining twenty-one, one per check (twenty-three tests, twenty-five drifts — two checks earn two rows each):

| Injected drift | Failure text (first line) |
|---|---|
| generated count 275 → 274 | `AGENTS.md plane 1 does not add up: 274 paths on disk, 40 registry-owned + 235 external-tool = 275` |
| `.gitignore` re-added to plane 2 | `AGENTS.md plane 2 … names ".gitignore", and nothing in the project plane answers to it.` |
| one schema contract dropped from plane 3 | `1 path(s) belong to the catalog plane and AGENTS.md plane 3 … names none of them: [registry/schema/snapshot.schema.json].` |
| plane 4 stops naming its guard | `AGENTS.md plane 4 must name TestEveryTrackedSourceFileHasAnOwner, the test that refuses an orphan` |
| `janitors` removed from the runtime span | `AGENTS.md says 18 runtime keys and its own span names 17` + `the Go model declares [janitors], and AGENTS.md names none of them.` |
| a second sentry-go importer | `AGENTS.md says internal/observability/sentryadapter/sentry.go is the ONLY github.com/getsentry/sentry-go importer; these import it too: [internal/observability/log/drift_scratch.go].` |
| a new `internal/` package with no bullet | `1 package(s) under internal/ appear nowhere in the AGENTS.md repo map: [internal/driftpkg].` |
| a `*.spec.ts` outside `e2e/` | `AGENTS.md says node lives ONLY under e2e/; these Node artefacts live elsewhere: [drift.spec.ts].` |
| `self_host` count left at 21 | `AGENTS.md says 21 self_host payloads today; the manifests declare 29.` |
| exit 5 dropped from the table | `modkit.ExitRollback = 5 and AGENTS.md's exit-code table does not document 5.` |
| a playbook recipe renamed | `the AGENTS.md task playbook names [Add a background job] and content/docs/extending.md has no such heading.` + `content/docs/extending.md carries 1 recipe(s) the AGENTS.md task playbook never names: [Add a job kind].` |
| app-group guards reordered in the doc | `AGENTS.md documents the /app group as [requireAuth requireNotDisabled loadPlan requireOrg]; appChain assembles [requireAuth requireNotDisabled requireOrg loadPlan].` |
| the `middleware.go` comment made stale again | `middleware.go's chain comment states 12 middlewares and Handler() assembles 14.` |
| a design rule dropped from the doc list | `the AGENTS.md design-system bullet names [… numeric brand step arbitrary length …] and designRules() enforces [… numeric brand step ! utility override arbitrary length …]` |
| a design rule renamed in the guard | `designRules() enforces "arbitrary length (disabled)" and this file carries no fixture for it.` + `this file carries a fixture for "arbitrary length" and designRules() enforces no such rule.` |
| the `hx-confirm` guard narrowed | `AGENTS.md says the hx-confirm guard skips [dev_ gallery scenario_]; the guard skips [admin dev_ gallery scenario_]. A guard that narrows without the document narrowing is a NEVER that stopped being one.` |
| signature count 174 → 173 | `AGENTS.md says 173 documented signatures; ui/reference_gen.go carries 174 Reference entries.` |
| a claims family removed from the doc | `AGENTS.md says 16 claims families and its own list names 15` + `claims families: the Go model declares [deploy], and AGENTS.md names none of them.` |
| `go_tools` removed from the dependency block | `dependency keys: the Go model declares [go_tools], and AGENTS.md names none of them.` |
| the notifications bullet drops its `notify` twin | `this repo-map bullet names internal/notifications and never mentions internal/notify: … Both exist. Name the twin on the same line, or an agent that greps the map edits whichever one it found.` |
| a repo-map path that does not exist | `the AGENTS.md repo map names "internal/scheduling" and no such path exists. Correct the bullet: stat ../../internal/scheduling: no such file or directory` |

## The 172 / 174 / 175 question, settled

`AGENTS.md` already carried the right pair after the audit; it is now mechanical. 174 is
`len(ReferenceRegistry)` in `ui/reference_gen.go` — the documented signatures. 175 is the exported
renderers in `package ui`, derived from the generated `*_templ.go` with `go/ast` rather than a
regexp over `.templ` sources, which is where 172 came from (a brace-naive scan that skipped the
three one-line `Opts` structs). The gap is one renderer, `CSRFField`, which `ui.Form` renders for
callers instead of exposing as a gallery component; the check requires the document to name every
member of that gap, so growing the allow-list forces the judgement to be written down. The stale
`172` comment in `designsystem_test.go:141` is outside this slice's subject and still says
"renderers" — worth a one-line fix by whoever next edits that file.

## AGENTS.md changes

43 insertions, 26 deletions. No claim was weakened; two were corrected (planes 2 and 3), and the
rest were made exact so the comparison could be exact rather than fuzzy:

- plane 2: `.gitignore` removed, with a sentence saying where it went.
- plane 3: rewritten as the format-owned set, 14 paths, authored/generated split stated.
- declaration paragraph: `plus identity metadata` → the seven keys named, so the key list needs no
  hand-written "identity metadata" table in the test.
- `self_host` paragraph: 21 → 29 payloads, three owning modules, and the rule stated (the module
  that owns the subject owns the assertion).
- repo map: `that file` → `observability/sentryadapter/sentry.go`; the `internal/notifications`
  bullet now names its `internal/notify` twin.
- design-system bullet: the seven prohibitions named verbatim as `designRules()` names them.
- task playbook: all 19 recipe headings plus the four topic headings, named exactly.

## Not mechanically checkable — the explicit residue

These claims are in the ranges the audit inventoried and are deliberately **not** gated:

1. **"macOS screenshots diff by design"** and **"CI's `visual` job"** — the first is an assertion
   about font rasterisation on a platform the gate does not run on; the second's "required" status
   is GitHub branch-protection state, which is not committed. Both flagged UNVERIFIABLE by the
   audit for the same reason.
2. **`standard-webhooks` confinement** (repo map, `internal/billing`) — not an ONLY claim. The
   library has four importers outside `billing/polar` (`internal/webhooks`, two in `internal/jobs`,
   one test fixture) and the sentence names two locations of the four. Gating it means writing an
   allow-list that is a policy decision, not a documented fact; the sentence as written is true
   (inbound verification is confined) and a set-equality check would have to invent the outbound
   allow-list. Left out rather than half-gated.
3. **Prose rationale** — "the order is load-bearing", "the shell must never be a swap target
   because clerk-js mounts there", "a hand edit survives until the next `make generate`, then
   vanishes silently". These are explanations of why a mechanism exists. The mechanisms are gated;
   the explanations are not assertions a test can read.
4. **"Every cross-cutting aggregate is RENDERED from module manifests"** — gated indirectly
   (`TestEveryEmittedPathIsRegistryOwned` already holds the emitters to the predicate); a second
   check would restate it.
5. **The `AGENTS.md` banner footnote** the audit raised (`AGENTS.md` quotes
   `Generated by ggg sync; DO NOT EDIT` inside the 4096-byte header window) is harmless today
   because `AGENTS.md` is not a name `IsRegistryOwnedOutputPath` returns true for. A check would
   have to assert a future emitter never claims a `content/docs/` page that quotes the banner in
   its first 4 KiB — a guard against a hypothetical, and the audit's own footnote says it is not a
   claim row.
6. **`ggg` invocations parsing against the command table** (audit M8/M15) — genuinely checkable,
   and out of this slice's eight items. Worth a follow-up: it would keep every command form quoted
   anywhere in `AGENTS.md` honest against `spec.go`.

## Two follow-ups found on the way

- `TestPublishedSchemasMatchModels`' `modelTypes` list omits `Dependencies`, `Requirement`,
  `ContractBounds`, `VendorArtifact`, `PersonaContribution`, `OpenAPIContribution`,
  `ProvisionerContribution`, `DatabaseOpsContribution` and `DeployContribution`, so those `$defs`
  are held to the Go model by nothing. `Dependencies` is now covered here and agrees; the rest are
  unmeasured. That list is the same shape of one-directional hole the audit kept finding.
- `internal/web/templates/designsystem_test.go:141` still says "the 172 renderers in ui/". 172 was
  a scanning artefact and the right number for "renderers" is 175. Outside this slice's subject,
  one line to fix.

## Runtime

**1.50 s** for all 23 tests, measured as the sum of per-test durations from `go test -v`
(`TestAgentsRepoMapExclusivityClaimsHold` is 0.81 s of it — one walk-and-read of every `.go` and
`.templ` under `internal/` and `cmd/`, cached so the four claims share one pass; everything else is
0.18 s or less). Well under the 5 s budget, and every test lives in a package `make check` already
compiles, so there is no new package to build. `make check` went from 2096 to 2119 passed tests
across 91 packages — exactly the 23 added, 0 skipped, 1 inapplicable.

## One thing the fixtures got wrong first

`input.css` carries `@source "internal/web/templates"`, so Tailwind scans that directory including
test files. The first version of the design-system fixtures wrote the forbidden utilities as
literals, and Tailwind compiled four of them into `static/app.css`; `make check` refused the
generation drift and named the file. The fixture class names are now assembled from fragments at
run time — the guard's regexps see the whole string, the scanner sees neither half — with the
reason recorded in the file. A fixture for a prohibition must not ship the thing it prohibits.

## Gates

- `go run ./cmd/ggg registry build` → `registry sign --dir . --key-file …` → `sync --offline` →
  `sync --check --offline`: clean, exit 0. Snapshot `d4a9aaab2abdff00…`, registry
  `4cb20e352351345d…`. The committed-signature gates are green: `TestPublishedRegistryIsConsistent`,
  `TestTheCommittedCoreSnapshotListsOnlyOwnedFiles`,
  `TestSignedRegistrySnapshotVerifiesPayloadsAndRejectsUnlistedFiles`,
  `TestEveryFileInThePublishedRegistryTreesHasADeclaringOwner`. (The brief named
  `TestCommittedSnapshotVerifiesUnderThePinnedCoreKey`; no test by that name exists in this
  repository — those four are the signature and snapshot-ownership gates.)
- `go test -race ./internal/modkit ./internal/gggcli ./internal/web ./internal/web/templates
  ./internal/web/templates/ui`: all pass (268 s / 10 s / 168 s / 4 s / 3 s).
- `make check`: green — `tests: 2119 passed, 0 skipped, 1 inapplicable, 0 failed across 91 packages`.
- `bin/ggg registry validate`: green — every core closure and the signed external `gadgetworks`
  fixture install, compile, test, remove and restore 2033 tree entries byte for byte.
- `make e2e` not run: nothing that renders changed. The two edits under `internal/web` outside
  tests are doc comments (`server.go`'s `Handler` chain, `middleware.go`'s package chain);
  `designsystem_test.go` gained one extracted helper, `ruleMatches`, and no behaviour.

Revisions bumped for the payloads that moved: `ggg/system/modkit` 75→77, `ggg/system/server` 22→24,
`ggg/element/ui-core` 6→7, `ggg/system/security` 9→10, `ggg/system/project-docs` 15→16.
