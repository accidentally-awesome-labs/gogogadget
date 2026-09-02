### C. Ship the maintained external-registry template

Binding plan requirement (verbatim, task 10):

> Add a maintained external-registry template containing registry metadata/signing, a seam
> adapter, a CLI contribution, contract tests, a local derivative fixture, and CI invoking
> `ggg registry validate`. Generated module/config/component references must show source
> namespace, contract range, provider slot, targets, automation level, dependency set,
> lifecycle, health, and verification commands.

Current state: the engine already supports signed external registries end to end (Task 4), and
`registry/external-testdata/` is a *test fixture* consumed by `internal/modkit/external_registry_test.go`
(namespace `acme`, canonical module `example.com/acme/gadget-registry`, a signed
`registry.snapshot.sig`, and one `mail-bridge` system module with contract tests). What does not
exist is the thing a third-party publisher copies: a maintained, standalone template repository
laid out in this repo, provably valid on every change.

#### What to build

1. **The template tree.** One directory (choose `templates/external-registry/` unless a better
   existing convention applies — say which you picked and why) containing a complete publishable
   registry:
   - `registry.json` with `schema`, `namespace`, `canonical_module`, `includes`.
   - The per-kind index files the loader expects.
   - **A seam adapter module**: one `system` module that provides an adapter for an existing
     provider slot, with `runtime.system.adapter` targets, owned env keys, dependency set,
     lifecycle and health declarations — everything a real adapter declares. Model it on a
     shipped adapter (e.g. `registry/modules/system/mail-smtp/module.json`) and on the
     `mail-bridge` fixture, but it must be its own module in its own namespace.
   - **A CLI contribution**: a `runtime.cli` entry (see `registry/modules/system/cli-ui/module.json`
     for the only shipped example: `{name, summary, package, handler}`), with the handler source
     as a module payload. It must respect the CLI boundary: contributed handlers receive
     controller operations, never `SecretValues`, provider clients or deploy clients.
   - **Contract tests** for the adapter, shipped as module-owned `test`-class payloads and
     declared in `tests`, so installing the module installs its proof.
   - **A local derivative fixture** that installs this template registry into a throwaway
     project and verifies it, in the shape the engine already uses for derivative verification.
   - **CI for the template**: a workflow inside the template that runs the publisher's gate —
     `ggg registry build`, `sign`/`verify`, and `ggg registry validate`.
   - Documentation of the publisher workflow: keygen → build → sign → verify → validate → tag,
     including that `sign` takes exactly one of `--key-file` or base64 `GGG_REGISTRY_SIGNING_KEY`
     (CI uses the env form) and that key rotation goes through `registry-key-rotation.json`.

2. **Ownership.** Every tracked file needs exactly one owner: `internal/modkit/ownership_test.go`
   (`TestEveryTrackedSourceFileHasAnOwner`) fails otherwise. Give the template tree an owning
   module (a `system` module in the core registry is the natural choice) rather than widening
   `projectOwned`. State the decision in your report.

3. **Reference completeness.** The generated module/config/component reference pages must show,
   for every module: source namespace, contract range, provider slot, targets, automation level,
   dependency set, lifecycle, health, and verification commands. Check
   `content/docs/module-reference.md` (generated) against that list and extend the generator
   where a field is missing — the template's whole point is that a consumer can read what an
   external module declares. Never hand-edit the generated page.

4. **Docs.** Per the repo's docs discipline, `content/docs/extending.md` gains the publisher
   path: where the template lives, what it contains, and the exact command sequence. Keep it
   short; the full docs recast is a separate task.

#### Acceptance

- `go run ./cmd/ggg registry validate` passes with the template's derivative fixture verified
  alongside the existing closures (install → compile → declared tests → remove → byte-for-byte
  restore).
- The template's signature verifies through the real path: `ggg registry verify` (or the
  equivalent noninteractive command) succeeds against the template's signed snapshot, and
  tampering with the payload, the signature or the namespace each refuses **before** any write.
  Add or extend tests for those three refusals if the existing fixture tests do not already
  cover them for a real (non-fixture) template.
- A test asserts the template stays valid: its manifests load, its declared adapter resolves the
  slot it claims, its CLI contribution name does not collide with a reserved built-in
  (`IsReservedName`), and its contract tests are declared.
- `internal/modkit`'s ownership test passes with the new tree.
- `go test ./internal/modkit ./internal/gggcli -count=1`, `go vet` and `gofmt` clean on touched
  packages; `go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline &&
  go run ./cmd/ggg sync --check --offline` clean.
- Do NOT run `make check`, the e2e suite, visual, or any project-wide formatter/linter.
