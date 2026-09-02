# Task C — maintained external-registry template

## Where the template lives and why

`templates/external-registry/`, the location the brief suggested. I considered
`registry/external-template/` to sit beside the existing `registry/testdata`
(example closures) and `registry/external-testdata` (the acme signature
fixture) and rejected it for three reasons:

1. `internal/modkit/ownership_test.go` already exempts everything under
   `registry/` as "the catalog plane". Putting the template there would have
   made it ownerless by accident, which is the opposite of what the brief
   asks for.
2. `BuildRegistrySnapshot` walks `registry.json` plus everything under
   `registry/`, so a template inside `registry/` would be pulled into the
   **core** repository's signed snapshot, and every re-sign of the template
   would perturb the core registry commit. Under `templates/` the core
   snapshot is untouched (`registry.snapshot.json` lists only `registry.json`
   and `registry/**`).
3. It is not catalog data. It is a tree a publisher copies. `registry/` holds
   what this repository publishes; `templates/` holds what someone else starts
   from.

The two existing trees under `registry/` stay what they are: `registry/testdata`
is the example-closure registry, `registry/external-testdata` is the
signature/provenance unit fixture. The template is the third thing — the
publishable artifact — and it is exercised by `ggg registry validate`, not only
by unit tests.

## Contents

```
templates/external-registry/
  registry.json                    schema 2, namespace "gadgetworks",
                                   canonical module example.com/gadgetworks/ggg-registry
  registry/{elements,components,pages,workflows,systems,profiles}.json
  registry/modules/system/audit-export-ledger/
    module.json                    the manifest
    ledger.go.txt                  the adapter payload
    ledger_test.go.txt             contract tests, class "test"
    cli.go.txt                     the `ggg ledger` handler
  registry.snapshot.json           deterministic signed catalog
  registry.snapshot.sig            base64 Ed25519 signature
  README.md                        publisher workflow (keygen→build→sign→verify→validate→tag)
  .github/workflows/registry.yml   the publisher gate
  .gitignore                       signing keys never enter version control
```

**The seam adapter.** `gadgetworks/system/audit-export-ledger` implements the
core `ggg/audit-export` slot (`audit.Exporter`). It was chosen over mail or
storage because the slot's interface is one method, so the template is about
*publishing shape* rather than about a large provider surface, while still
being a real slot in the shipped catalog with real adapters to replace.

It declares everything a real adapter declares:

- `runtime.system.adapter` with two service targets — `ledger-file`
  (`mode: development`, environments `development`+`test`, no account) and
  `ledger-cloud` (`mode: managed`, environments `production`,
  `console_url`, two `inputs` mapped to env keys). Between them they cover all
  three environments, which is what lets a single external module fill a slot.
- three owned env keys (`GADGETWORKS_LEDGER_DIR|ENDPOINT|TOKEN`), each scoped
  with `targets: ["gadgetworks/system/audit-export-ledger@…"]` so `required`
  is enforced only for the active selection.
- `dependencies.go: [github.com/stretchr/testify v1.11.1]`, so install/remove
  exercises the real dependency path (the version already exists in this
  repo's `go.mod`, so ownership moves without a version change).
- `requires` with contract ranges, including `ggg/system/modkit` at `[2,2]` —
  the CLI handler consumes that contract, and modkit publishes contract 2.
- `stop: true` (idempotent, context-bounded, matching the `internal/db` Stop
  pattern) and `health: true`.
- `tests.go_packages` + `tests.capabilities`, with the contract tests shipped
  as a `class: "test"` payload, so installing the module installs its proof.
- no fallback: a `ledger-cloud` selection without a token fails at
  `NewModule` naming the key, and never writes the audit ledger to a file.

**The CLI contribution.** `runtime.cli` contributes `ggg ledger`
(`{name, summary, package, handler}` + `claims.cli`). The handler reaches the
project only through `cc.Controller.Execute(ctx, gggcli.InfoRequest{…})`; it
imports `context`, `fmt`, `sort`, the rewritten `internal/gggcli` and
`internal/modkit`, and its own `example.com/gadgetworks/ggg-registry/…`
package — the latter proving the multi-canonical-prefix import rewrite on the
real install path. `TestExternalRegistryTemplateHandlerRespectsTheCLIBoundary`
runs the real `ValidateCLIHandlerPackages` scan over the rewritten payload
bytes (no `net/http`, no `os/exec`, no provider seams, no `SecretValues`), and
asserts the handler package was actually scanned rather than skipped.

**The derivative fixture.** `ValidateExamples` now exercises the template as a
closure of its own, alongside the existing nine. The mechanics reuse the proven
provider-permutation path rather than duplicating it:

- `providerFixtureSpec` changed from a `local`/`managed` pair to a
  `candidates []string` list, because one adapter with two targets can cover
  every environment — exactly the template's shape. The two existing fixtures
  are expressed in the new shape unchanged.
- `exampleClosure` gained `registry *externalRegistrySpec` and `testRun`.
  A closure with a registry is **not** vendored into the derivative's own
  catalog (`publishExamples`); instead `attachExternalRegistry` verifies the
  signed snapshot with `VerifyRegistrySnapshot(dir, publicKey)` — the same code
  path `ggg registry verify` runs — and then adds
  `{namespace: "gadgetworks", source: "directory", path: "templates/external-registry"}`
  to the derivative's `gogogadget.json`. Removal is the baseline-project
  restore that the provider path already performs.
- `testRun` exists because the example registry filters to `^TestExample`
  (its packages are shared with the shipped suite). The template's packages are
  exclusively its own, so it runs every test it declared.
- `externalTemplateClosures` loads the template through `LoadCatalog`, checks
  its declared identity, and `assertExternalTemplateUnreachable` refuses if the
  shipped project ever configures the `gadgetworks` namespace or the committed
  lock ever installs a template module — the same isolation guard the examples
  have.

Note the signature is verified out-of-band on purpose: a `directory` registry
source **forbids** `public_key` in `gogogadget.json` by design (only remote
registries must be signed). Resolution of a directory source still verifies
every payload digest and refuses an unlisted file under `registry/`; what it
cannot do alone is bind the tree to a publisher key, so the harness does what
the operator's `ggg registry verify` does, before it writes anything. The
verified digest is reported in the envelope
(`published by registry gadgetworks at signed snapshot …`).

**The demonstration key.** Published on purpose:
`ZKuGJnL3y0a1TwHe4Jax0T2pVD9eb7C0vEfgHAFGEsQ=`, private half =
`sha256("gogogadget external-registry template demonstration key")`. That is
what lets this repository re-sign the template and compare bytes, and lets any
reader re-derive and check the signature. Both the README and
`TestExternalRegistryTemplateSignatureIsReproducible` state it; the test also
fails if the README stops publishing the key or the seed phrase.

## Ownership decision

`ggg/system/registry-template` (new, in the core registry) owns all 16 files
under `templates/external-registry/`, source == target, `rewrite_module:
false`, class `asset` except `README.md` which is `docs`. It has no runtime, no
claims, `removal_policy: free`, and is a member of `ggg/profile/full` only
(`full` is "every module the catalog publishes"; minimal/web/api/saas do not
carry it).

`projectOwned` in `internal/modkit/ownership_test.go` was **not** widened. The
payloads are `.go.txt` rather than `.go` so this repository never tries to
compile source written against a consumer's module path.
`TestExternalRegistryTemplateTreeHasExactlyOneOwner` asserts every file in the
tree resolves to exactly one owning module, independently of the global
ownership sweep.

The two new test files (`internal/modkit/external_template_test.go`,
`internal/gggcli/external_template_test.go`) are owned by `ggg/system/modkit`,
which already owns `internal/modkit` and the nonvisual `internal/gggcli`
(revision 7 → 8).

## Reference completeness

`content/docs/module-reference.md` previously had one table per kind with
`Module | Title | Requires | Removal`. Of the nine required facts, **only one**
existed (a partial one: `Requires` without contract ranges). Added:

| Fact | Where it now appears |
|---|---|
| Source namespace | new `Source` column in every per-kind table, from the lock's `registry_namespace` (provenance, not an id prefix). Also added to the config and component references. |
| Contract range | `Requires` now renders `` `id` [min,max] ``; a new `Contract` column shows the contract the module publishes |
| Provider slot | new `## Provider slots` section: slot, declaring module, `critical`, capability/type pairs |
| Targets | new `## Provider adapters` section: one row per `ADAPTER@TARGET` with mode and environments |
| Automation level | `Automation` column in that section (plus the database operator where declared) |
| Dependency set | new `## Dependencies` section: Go modules with versions, Go tools, containers |
| Lifecycle | new `## Lifecycle and health` section: `Start`/`Stop` per module |
| Health | `Health` column in that section |
| Verification commands | new `## Verification` section, plus declared test capabilities |

To keep one answer to "how do I verify this module", `verificationCommands`
moved out of `internal/gggcli/executors.go` and became exported
`modkit.VerificationCommands`; `ggg info` and the generated page now call the
same function (the CLI copy was deleted, not aliased). A shared
`registryNamespaces(lock)` helper feeds all three reference emitters.

`ggg registry build` gained `--dir` (`Controller.registryBuildDir`,
project-relative, refuses absolute/`..`/untrimmed paths). Without it a registry
that is not the project root could not be built at all — which the template's
own CI needs and which is how the committed template is maintained here. The
`--dir` flag was already declared on the `registry` command spec for
`init|sign|verify|rotate`; only `build` ignored it.

`content/docs/extending.md` gained a short `## Publish your own registry`
section: where the template lives, what it contains, the exact command
sequence, the `sign` exactly-one-of rule, the consumer side, the refusal list,
and key rotation.

## Commands run and output

```
$ go run ./cmd/ggg registry build --dir templates/external-registry
(no output — success)

$ GGG_REGISTRY_SIGNING_KEY=… go run ./cmd/ggg registry sign --dir templates/external-registry
snapshot sha256 ef2311d14c008ca7c65dc2b0486a3c2332afde70a6731581fb562d165c4afde3

$ go run ./cmd/ggg registry verify --dir templates/external-registry \
    --public-key ZKuGJnL3y0a1TwHe4Jax0T2pVD9eb7C0vEfgHAFGEsQ=
snapshot sha256 ef2311d14c008ca7c65dc2b0486a3c2332afde70a6731581fb562d165c4afde3

$ go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline
registry 904ff43250459915fadb4ab64bb00c0ab7a19a9c512419f7335041035c0888c4
  update    lock       gogogadget.lock.json

$ go run ./cmd/ggg sync --check --offline
registry 904ff43250459915fadb4ab64bb00c0ab7a19a9c512419f7335041035c0888c4
(clean — no drift)

$ go test ./internal/modkit ./internal/gggcli -count=1
ok  	github.com/gogogadget/gogogadget/internal/modkit	7.802s
ok  	github.com/gogogadget/gogogadget/internal/gggcli	0.560s

$ go vet ./internal/modkit ./internal/gggcli
(clean)

$ gofmt -l internal/modkit internal/gggcli content
(clean; the three .go.txt payloads are gofmt-clean when copied to .go)

$ go run ./cmd/ggg registry validate
…
gadgetworks/system/audit-export-ledger
  closure: gadgetworks/system/audit-export-ledger
  registry gadgetworks verified at signed snapshot ef2311d1…5c4afde3
  installed 3 file(s)
  compiled ./... and generated 30 file(s)
  module tests passed in ./internal/gadgetworks/ledger
  removed; 1890 tree entries restored, 25 aggregate(s) differ only in the
  lock-identity header, 0 migration(s) retained
  info example_closure_verified system closure gadgetworks/system/audit-export-ledger:
       installed 3 file(s), regenerated 30, compiled, 1890 tree entries restored
       byte for byte, published by registry gadgetworks at signed snapshot ef2311d1…
EXIT=0
```

All ten pre-existing closures still pass in the same run. Because the template
closure is registered as a provider permutation, the run also switched
`ggg/audit-export` to the external adapter in all three environments, removed
`audit-export-noop` and `audit-export-otlp`, observed the generated bootstrap
calling the external constructor exactly once per environment branch, and
proved the three provider refusals (missing selection, development target in
production, removal of a selected adapter) before restoring everything.

### Template-specific tests

`internal/modkit/external_template_test.go`:

- `TestExternalRegistryTemplateStaysValid` — manifests load; namespace and
  canonical module match; the adapter's capability set is exactly the core
  slot's; targets cover dev/test/production with no development target in
  production; env keys are owned and target-scoped; every `requires` resolves
  in the core catalog **inside** its declared contract range; `stop`/`health`
  declared; declared test packages are claimed and ship a class-test payload;
  the contributed command is claimed and its handler package is claimed;
  payload bytes match the pinned digests.
- `TestExternalRegistryTemplateHandlerRespectsTheCLIBoundary`
- `TestExternalRegistryTemplateSnapshotIsSignedAndTamperEvident` — rebuilt
  snapshot equals the committed bytes; `VerifyRegistrySnapshot` succeeds and
  its digest is the snapshot's sha256; then the three required refusals plus a
  fourth: **tampered payload** → `digest mismatch`; **tampered signature** →
  `signature verification failed`; **tampered namespace** → `digest mismatch`
  for the rewritten root, `namespace` for a mismatched pin via
  `validateGitHubSnapshot`, and `namespace` via the real
  `DirectorySource.Resolve`; **unlisted payload** → `unlisted`. Every refusal
  is asserted against an in-memory `fstest.MapFS`, so a passing case
  demonstrably wrote nothing.
- `TestExternalRegistryTemplateSignatureIsReproducible` — key derives from the
  documented seed; the committed signature is the seed-derived signature over
  the committed snapshot; the README publishes the key, the seed phrase and
  every publisher command; the CI workflow runs build/sign/verify/validate with
  the env-form signing key.
- `TestExternalRegistryTemplateIsRebuildable` — `RefreshManifestDigests`
  reports nothing stale, and rebuilt indexes + re-signed snapshot are
  byte-identical to the committed tree, so the template's own CI step that
  refuses a dirty tree after `registry build` is passable.
- `TestExternalRegistryTemplateTreeHasExactlyOneOwner`

`internal/gggcli/external_template_test.go`:

- `TestExternalTemplateCommandDoesNotShadowABuiltIn` — `IsReservedName` is
  false for `ledger`, and the contributed command joins the real command table
  with no conflicts and is dispatchable by name. (This lives in `gggcli`
  because `modkit` cannot import it — the reserved table belongs to the CLI.)
- `TestExternalTemplateVerificationCommandsAreRunnable`
- `TestRegistryBuildDirIsProjectContained`

## Commits

- `fbfbfaa` — feat(registry): ship the maintained external-registry template

### One extra fix the task required

The root `.gitignore` ignored `registry.snapshot.sig` unanchored, so it
silently refused to commit the template's signature — and the core manifest
pins that file's digest, so a fresh clone would have failed `sync` with a
missing payload. The rule is now `/registry.snapshot.sig` with a comment
explaining that a distributed registry's signature is a published artifact,
not a local build product.

## Concerns

1. **Maintenance order is load-bearing.** Editing anything under
   `templates/external-registry/` requires, in order:
   `ggg registry build --dir templates/external-registry` →
   `GGG_REGISTRY_SIGNING_KEY=… ggg registry sign --dir templates/external-registry`
   → `ggg registry build` → `ggg sync --offline`. The template's snapshot and
   signature are payloads the core manifest pins, so signing before the core
   build is mandatory or the core sync fails with `sha256 mismatch`.
   `TestExternalRegistryTemplateSnapshotIsSignedAndTamperEvident` and
   `TestExternalRegistryTemplateIsRebuildable` catch a missed step, and the
   first one's failure message names the command to run.
2. **The demonstration key is published.** That is deliberate and documented in
   three places, but it does mean a copy of the template that is published
   without running `ggg registry keygen` would be signed by a key anyone has.
   The README says so twice; nothing in the tooling can detect it, because a
   registry cannot know whether its own key is secret.
3. **`ggg registry validate` now takes ~2.5 minutes** (ten closures plus the
   template). It was already an operator-invoked command with a pid lock rather
   than a Make target, so this is a longer run of the same shape, not a new
   constraint.
4. **The template's CI workflow is not executed by this repository.** It runs
   `go install …/cmd/ggg@latest` in a publisher's repository, which cannot work
   inside this one. Its content is asserted by test (build/sign/verify/validate
   plus the env-form signing key), and every command it runs is exercised here
   directly, but the YAML itself is only proven by a publisher who copies it.
5. **`content/docs/extending.md` is otherwise stale** — its "Author a module"
   example still shows `"schema": 1` and string `requires`. I left that alone:
   the full docs recast is a separate task and the brief said to keep this
   addition short.
