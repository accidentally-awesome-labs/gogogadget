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

---

# Fix round 1 — response to task-c-review-findings.md

All six items fixed. `go run ./cmd/ggg registry validate` → `EXIT=0`, 11
closures; the template closure now installs 4 files (the new constants-only
package) and its declared tests still pass inside the derivative, which is
what proves the config-struct-consuming constructor compiles and its refusals
fire in a real project.

## C1 — the CI workflow could not run

Rewritten end to end (`templates/external-registry/.github/workflows/registry.yml`).
The three refusals the reviewer traced are gone, and the workflow now proves
what its step names claim:

- **`ggg new` had no `--registry`.** It now passes
  `--registry "github:${GGG_REPOSITORY}" --ref "${GGG_REF}"`, with `GGG_REF`
  a pinned framework release used for both `go install …@${GGG_REF}` and the
  registry ref. `parseNewRegistry` embeds the core public key for a GitHub
  source, so only the ref has to be named, and a development build refusing
  without an explicit ref is handled by pinning rather than hoped past.
  `--module`, `--profile` and `--non-interactive` are asserted too.
- **`registry add directory:$GITHUB_WORKSPACE` was absolute.** The checkout
  moved to a `registry/` path, and the consumer step copies only the published
  artifacts (`registry.json`, `registry/`, the snapshot, the signature) into
  `registries/gadgetworks/` inside the consumer, then adds
  `directory:registries/gadgetworks` — project-contained, no shell expansion.
- **`ggg registry validate` in the scratch consumer was an always-green
  no-op.** Removed, with a header comment stating why (it exercises the
  closures a configured registry publishes and finds them by looking for known
  registry roots inside the project it runs from; a scratch consumer has
  none). Replaced with the steps that are actually observable there:
  `ggg setup`, `registry add`, `provider set`, `sync --check` (a second
  reconcile must move nothing), `generate`, `go build ./...`,
  `go test -count=1` of the declared package, `bin/ggg ledger` after
  rebuilding `bin/ggg` from the consumer's own regenerated source (which is
  what proves the CLI contribution was generated), and finally a removal check
  that restores the core adapters, removes the module, asserts
  `internal/gadgetworks` is gone and the consumer still builds.

## C2 — the guard could not have caught C1

`assertTemplateWorkflowIsRunnable` replaces substring presence with the
assertions that map to real refusals: the `ggg new` invocation must carry
`--registry`, `--ref`, `--module`, `--profile` and `--non-interactive`; every
`directory:` argument in the file must not begin with `/` or `$`; the workflow
may only mention `ggg registry validate` if it also carries the comment
explaining it is deliberately not run there; and it must contain the real
`registry add` / `provider set` / `go build ./...` / `go test -count=1` steps,
so deleting the honest proof and leaving a green no-op fails the test.

## I1 — the exemplar selected its target by credential presence

This was the `ResendConfigured` shape the plan ordered deleted, in the one file
whose job is to be copied. Fixed by following `internal/mail/resend`:

- `Deps` is now `struct{ Config *config.Config }` and the manifest declares
  `needs: [{Config, config, *config.Config}]` plus the `internal/config` type
  import, so the adapter reads the same declarations that produced
  `.env.example` and the configuration reference instead of going behind the
  parser with `host.Env`. `ggg/system/config` joined `requires`. The generated
  config validation already enforces `required` per active target, so the
  constructor's refusal is a second statement of the same key names, not the
  only one.
- Target selection is now `SelectedTarget(environment)`: the manifest lists the
  two targets under **disjoint** `environments`, so the environment the runtime
  booted into *is* the project's recorded selection. Nothing is inferred from
  which credentials happen to be set.
- A production selection with a missing endpoint or token returns a boot error
  naming the key and the target, and returns a nil module — it can no longer
  fall through to the file exporter.
- `TestNewModulePrefersTheEndpointOverTheLocalFile` became
  `TestSelectedTargetFollowsTheEnvironmentNotTheCredentials`, which asserts the
  inverse of the old behaviour: a development run holding a full set of cloud
  credentials still constructs the file target.
  `TestNewModuleRefusesAnIncompleteManagedSelection` now covers both missing
  keys and asserts the module is nil; two new tests cover the missing config
  dependency and a complete production selection.
- Both prose claims were re-aligned and now say *how* the guarantee holds
  (disjoint environments + config struct), in `ledger.go.txt`'s package
  comment and in the README.

## I2 — the handler was one hop from an `*http.Client`

New payload `meta.go.txt` → `internal/gadgetworks/ledger/meta/meta.go`, a
constants-only package holding `ModuleID`, `Slot`, `TargetFile`,
`TargetCloud`, the three env keys and `DefaultDir`. The handler imports `meta`;
the adapter imports `meta`; the handler no longer imports the adapter. The
package comment says exactly why, so a publisher copying the split knows it is
load-bearing rather than cosmetic.

`assertTemplateHandlerImportsAreCleanOneHop` extends the boundary test: it
groups the module's payloads by claimed package, parses the handler's imports,
resolves each one that lands inside **this module's own** packages, and checks
that package's direct imports against `matchBannedImport`. Core packages under
the same rewritten prefix are skipped (they are the framework's own, covered by
the shipped direct scan, and the scan's general non-transitivity is explicitly
out of scope). It fails if it reached nothing, so it cannot pass vacuously.

## M1–M4

- **M1** `exerciseProviderClosure` deleted; `exerciseExampleClosure` calls
  `exerciseStandardClosure` directly with a comment saying there is one
  exerciser and it derives the spec from the closure id.
- **M2** `providerChoicesFromModules` now refuses two candidate targets
  claiming one environment, naming both, so disjointness is enforced instead of
  incidental.
- **M3** `content/docs/extending.md` names the alternative before the
  sequence: if you copied the template you already have a `registry.json` —
  edit `namespace`/`canonical_module` and skip `registry init`.
- **M4** README step 7 and the extending page now state what
  `ggg registry validate` actually exercises, that this repository runs it
  against the template, and give the scratch-consumer sequence a publisher
  should run instead — the same one the workflow runs.

## Commands run (fix round)

```
$ go run ./cmd/ggg registry build --dir templates/external-registry
$ GGG_REGISTRY_SIGNING_KEY=… go run ./cmd/ggg registry sign --dir templates/external-registry
snapshot sha256 fa788a44733b94f6b6942662eb0ce4d43efe279097153a7add99fca275ade988
$ go run ./cmd/ggg registry verify --dir templates/external-registry --public-key ZKuGJ…
snapshot sha256 fa788a44733b94f6b6942662eb0ce4d43efe279097153a7add99fca275ade988
$ go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline
registry 29e1d63da2f61a14a4873ceb2ef167c58ad6716f82108dc1bd35c1cea74fd92d
$ go run ./cmd/ggg sync --check --offline
registry 29e1d63d… (clean)
$ go test ./internal/modkit ./internal/gggcli -count=1
ok internal/modkit 7.077s / ok internal/gggcli 0.439s
$ go vet ./internal/modkit ./internal/gggcli    -> VET_OK
$ gofmt -l internal/modkit internal/gggcli content -> clean
$ go run ./cmd/ggg registry validate
gadgetworks/system/audit-export-ledger
  registry gadgetworks verified at snapshot fa788a44…
  installed 4 file(s); compiled; module tests passed in ./internal/gadgetworks/ledger
  removed; 1891 tree entries restored byte for byte
EXIT=0  (11 closures)
```

## Commits (fix round)

- `80ebb22` — fix(registry): make the template'''s publisher gate runnable and its adapter refuse

## Remaining honest caveat

The workflow itself is still executed only by a publisher, not by this
repository — a CI file that installs `ggg` from a release into a fresh
consumer cannot run inside the framework repo. What changed is that every
command in it is now one that would succeed: the three specific refusals are
removed, and the guard test asserts the shapes that caused them rather than
their names. `GGG_REF: v0.1.0` is a placeholder release tag a publisher pins
to the framework version they tested against.

---

# Fix round 2 — response to the re-review blockers

All three blockers were inside the removal check I added in round 1 to replace
the deleted no-op. Every one of them was the same mistake in a new place:
asserting a shape I had not traced through the engine.

## B1 — `ggg remove` can never remove an adapter

`bin/ggg remove "${MODULE_ID}"` was unrunnable in both orderings, and the
reason is structural rather than incidental. `registry add` edits
`Project.Registries` only, so the module never enters `Project.Modules`: it is
in the graph solely because a provider choice names it (`resolve.go` gives it
reason `provider`). So:

- **Before deselection** — removing a still-selected adapter is a designed
  refusal (`resolve.go:308-309`), which this repository's own harness asserts
  in `expectProviderRefusals` (`example.go:976-978`).
- **After deselection** — `retiredAdapters` (`plan.go:581-609`) already folded
  the replaced adapter's files out in the same transaction as the new
  selection, so the lock no longer lists the module and `OpRemove` refuses
  "not installed".

Deselection *is* the removal. The step is now "Prove deselection removes the
adapter": the `provider set` back to the core adapters, then assertions that
the installed files are gone and `go build ./...` is still green. Dropping the
registry source is its own step (`ggg registry remove ${REGISTRY_NAMESPACE}`
followed by `sync --check`), which is a different operation on a different
object and does succeed. The step comment states the two refusals so a
publisher copying it does not re-add the line.

`README.md`'s "plus a removal check" claim is replaced with the explanation:
deselection is the reverse for an adapter, `ggg remove` refuses in either
ordering, assert on files, and the source comes out with
`ggg registry remove NAMESPACE`.

## B2 — `test ! -d` fails after a correct removal

`Engine.Apply` deletes files (`apply.go:228-232`); directory pruning exists
only in the rollback closure (`apply.go:143-164`). So
`internal/gadgetworks/`, `.../ledger/`, `.../ledger/meta/` and
`.../ledger/cli/` all survive a correct removal, and the assertion would have
failed on success. Replaced with four `test ! -f` assertions naming the exact
installed payload targets.

## B3 — the guard was presence-shaped, which is why B1 and B2 landed under it

`assertTemplateWorkflowIsRunnable` now takes the module manifest and adds two
assertions in the same style as the round-1 ones:

- No line invoking `ggg remove` may name the adapter (by id, `${MODULE_ID}` or
  `$MODULE_ID`); the failure message says an adapter leaves by deselection.
  `registry remove`, a different command on a different object, is not matched.
- No `test ! -d` may name a directory derived from the module's installed
  file targets — the set is computed from `module.Files`, walking each target's
  parents, so adding a payload extends the guard automatically. The failure
  message says apply removes files and leaves the directory behind.

Both were verified to fail on the exact lines that landed, by re-adding them
to the workflow and running the test:

```
external_template_test.go:447: the template CI workflow removes the adapter with
  `ggg remove` in "bin/ggg remove \"${MODULE_ID}\" --json"; an adapter leaves by deselection
external_template_test.go:447: the template CI workflow asserts
  "test ! -d internal/gadgetworks/ledger", but apply removes files and leaves the
  directory behind; assert on the installed files
```

## Commands run (fix round 2)

```
$ go run ./cmd/ggg registry build --dir templates/external-registry
$ GGG_REGISTRY_SIGNING_KEY=… go run ./cmd/ggg registry sign --dir templates/external-registry
snapshot sha256 fa788a44733b94f6b6942662eb0ce4d43efe279097153a7add99fca275ade988
$ go run ./cmd/ggg registry verify --dir templates/external-registry --public-key ZKuGJ…
snapshot sha256 fa788a44733b94f6b6942662eb0ce4d43efe279097153a7add99fca275ade988
$ go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline
registry 6836234c0e309f93887c7f96baff5c4c58c26f10d857ff96d3752b49558d735b
$ go run ./cmd/ggg sync --check --offline
registry 6836234c… (clean)
$ go test ./internal/modkit ./internal/gggcli -count=1
ok internal/modkit 7.500s / ok internal/gggcli 0.449s
$ go vet ./internal/modkit ./internal/gggcli    -> VET_OK
$ gofmt -l internal/modkit internal/gggcli content -> clean
$ go run ./cmd/ggg registry validate
EXIT=0, 11 closures; gadgetworks/system/audit-export-ledger: installed 4 file(s),
regenerated 30, compiled, 1891 tree entries restored byte for byte, published by
registry gadgetworks at signed snapshot fa788a44…
```

The template's signed snapshot digest is unchanged (`fa788a44…`) because the
workflow and the README are not part of it — `BuildRegistrySnapshot` lists
`registry.json` and `registry/**` only. The core registry commit did move,
because the core manifest pins those two files' digests as payloads.

## Commits (fix round 2)

- `556ce74` — fix(registry): remove the adapter by deselection in the template's gate
