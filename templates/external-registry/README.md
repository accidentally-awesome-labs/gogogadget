# External registry template

A complete, signed, publishable GoGoGadget registry. Copy this directory to
the root of a new repository, rename the namespace, and you are a publisher:
consumers add your registry to their project, install your modules as source
they own, and keep updating without losing local edits. You never fork the
core catalog, and the core catalog never learns your name.

This tree is maintained in the framework repository, so it is proven on every
change rather than documented and left to rot:

- `TestExternalRegistryTemplateStaysValid` loads the manifests, resolves the
  slot the adapter claims, checks the contributed command name against the
  reserved built-ins, and asserts the declared contract tests exist.
- `TestExternalRegistryTemplateSnapshotIsSignedAndTamperEvident` rebuilds the
  snapshot with the deterministic codec, verifies the committed signature,
  and proves that a tampered payload, a bad signature, and a wrong namespace
  each refuse before any write.
- `ggg registry validate` installs this registry into a throwaway derivative
  of a real project, compiles it, runs the declared tests, selects it for the
  `ggg/audit-export` slot in all three environments, removes it, and asserts
  the tree came back byte for byte.

## What is in here

```
registry.json                 namespace, canonical Go module, kind includes
registry/*.json               one index per module kind
registry/modules/system/audit-export-ledger/
  module.json                 the manifest: files, requires + contract ranges,
                              dependencies, adapter slot + service targets,
                              environment keys, lifecycle, health, tests
  ledger.go.txt               the adapter payload
  ledger_test.go.txt          its contract tests, class "test"
  meta.go.txt                 constants-only identity: id, slot, targets, env keys
  cli.go.txt                  the `ggg ledger` command handler
registry.snapshot.json        the deterministic signed catalog
registry.snapshot.sig         base64 Ed25519 signature over those exact bytes
.github/workflows/registry.yml the publisher gate
```

### Identity

| Field | Value |
|---|---|
| Namespace | `gadgetworks` |
| Canonical Go module | `example.com/gadgetworks/ggg-registry` |
| Public key | `ZKuGJnL3y0a1TwHe4Jax0T2pVD9eb7C0vEfgHAFGEsQ=` |

The namespace scopes every id you publish (`gadgetworks/system/…`), so two
registries can ship a module of the same name without shadowing each other.
The canonical Go module is the prefix your payloads import their own packages
under; on install every canonical prefix — yours and the core one — is
rewritten to the consumer's module path, which is how an adapter imports both
`example.com/gadgetworks/ggg-registry/internal/gadgetworks/ledger` and
`github.com/gogogadget/gogogadget/internal/audit` and still compiles inside
someone else's project.

**The key in this template is a demonstration key.** Its seed is
`sha256("gogogadget external-registry template demonstration key")`, which is
what lets the framework's own tests re-sign this tree and compare bytes. Run
`ggg registry keygen` before you publish anything real, and never commit the
private half.

### The module

`gadgetworks/system/audit-export-ledger` adapts the core `ggg/audit-export`
slot. It is the shape every adapter has:

- one seam implementation (`audit.Exporter`) and nothing else provided;
- two service targets — `ledger-file` (`mode: development`, allowed in
  `development` and `test`, no account) and `ledger-cloud`
  (`mode: managed`, allowed in `production`);
- three owned environment keys, each mapped to a target input, so
  `ggg provider configure` can render the form and `.env.example` and the
  generated configuration reference list them without a hand edit;
- `stop: true` and `health: true`, because it buffers a file handle and talks
  to a managed service — the lifecycle must be idempotent and
  context-bounded, and the health check must fail rather than degrade
  silently;
- `dependencies.go`, so the consumer's `go.mod` is updated on install and the
  requirement is dropped again on removal if nothing else owns it;
- `tests.go_packages`, so installing the module installs its proof.

There is no fallback path, and no credential sniffing. The manifest lists the
two targets under **disjoint** `environments`, so the constructor derives the
selected target from `Config.Env` — a development run with a full set of cloud
credentials still writes the local file, and a production run without an
endpoint or a token fails at boot with the key named. It never quietly writes
a compliance ledger to a file in `tmp/`. The adapter reads those values off
the generated config struct, which is the same declaration that produced
`.env.example` and the configuration reference.

### The command

`runtime.cli` contributes `ggg ledger`. Contributed handlers are data at
install time and run only when an operator types the command. A handler's
only route back into the project is `cc.Controller`, so it can execute a
read or preview a mutation but can never hold secret values, build a
provider client, reach a deploy client, or shell out — `net/http`,
`os/exec`, and the provider seams are refused inside a handler package at
sync time. Built-in command names are reserved; a contributed name that
collides is reported and skipped.

The handler imports the constants-only `meta` package rather than the adapter
package. That is deliberate: the adapter holds an `*http.Client` and a bearer
token, and a handler one hop away from a provider client has routed around the
boundary rather than respected it — even though the sync-time scan reads only
direct imports. Copy that split.

## Publisher workflow

Run these from the root of your registry repository. `ggg` comes from the
framework:

```sh
go install github.com/gogogadget/gogogadget/cmd/ggg@latest
```

### 1. Scaffold (once)

```sh
ggg registry init --namespace acme --canonical-module example.com/acme/ggg-registry
```

Writes `registry.json`, one empty index per kind, and a `.gitignore` that
keeps signing keys out of version control. Existing files are never
overwritten, so re-running it on a lived-in registry is safe.

### 2. Keygen (once)

```sh
ggg registry keygen --private registry-private-key.b64 --public registry-public-key.b64
```

Writes the private key `0600` and the public key `0644`, and refuses to
overwrite either path. Publish the **public** key: it is the string consumers
pin as `public_key` in their `gogogadget.json`. Store the private key in your
CI secret store as base64 and nowhere else.

### 3. Author a module

One directory per module under `registry/modules/<kind>/<name>/`, with
`module.json` beside its payloads. Payloads are committed as `*.txt` so your
own repository does not try to compile source written against a consumer's
module path. Declare every file with a `target`, a `class`, and a digest;
`registry build` computes the digests for you.

### 4. Build

```sh
ggg registry build --dir .
```

Refreshes every manifest's payload digests, rewrites each kind index from
what is actually on disk, and writes `registry.snapshot.json` — the
deterministic listing of the registry root, the indexes, the manifests, and
the SHA-256 of every payload.

### 5. Sign

```sh
# locally
ggg registry sign --dir . --key-file registry-private-key.b64

# in CI
GGG_REGISTRY_SIGNING_KEY="$(printenv REGISTRY_SIGNING_KEY)" ggg registry sign --dir .
```

`sign` accepts **exactly one** of `--key-file` or the base64
`GGG_REGISTRY_SIGNING_KEY` environment variable, and refuses when both or
neither are present. CI uses the environment form so the key never touches a
disk. It writes `registry.snapshot.sig`: a base64 Ed25519 signature over the
exact snapshot bytes.

### 6. Verify

```sh
ggg registry verify --dir . --public-key "$(cat registry-public-key.b64)"
```

Checks the signature under the pinned public key, then every listed payload
digest, and refuses an unlisted file under `registry/`. This is the same code
path a consumer's `ggg registry add` runs, so a green `verify` is the promise
that an install will not refuse.

### 7. Validate

`ggg registry validate` is the framework's lifecycle proof — install,
generate, compile, run the module's declared tests, remove, compare the tree
byte for byte — but it exercises the closures a *configured* registry
publishes, and it finds them by looking for known registry roots inside the
project it is run from. Run in your registry repository, or in a scratch
consumer, it has nothing to exercise and exits 0 having proven nothing. The
framework repository runs it against this template on every change; that is
what keeps the template itself honest.

What you run is the same claim, made where it is observable: install into a
throwaway consumer and exercise it there.

```sh
ggg new /tmp/consumer --module example.com/consumer \
  --profile ggg/profile/minimal \
  --registry github:gogogadget/gogogadget --ref v0.1.0 \
  --non-interactive --json
cd /tmp/consumer && ggg setup

# A directory source must be project-contained, so copy the registry in.
mkdir -p registries/acme
cp -R /path/to/registry/{registry.json,registry,registry.snapshot.json,registry.snapshot.sig} registries/acme/
bin/ggg registry add directory:registries/acme --namespace acme --json

bin/ggg provider set --provider ggg/audit-export:production=acme/system/audit-export-ledger@ledger-cloud …
bin/ggg sync --check                 # a second reconcile must move nothing
bin/ggg generate && go build ./...
go test -count=1 ./internal/acme/ledger
```

`.github/workflows/registry.yml` runs exactly this, then proves the reverse.
Note what "the reverse" is for an adapter: it is **deselection**, not
`ggg remove`. `registry add` only edits the registry list, so your module
never enters `modules` in `gogogadget.json` — it is in the graph solely
because a provider choice names it. Selecting a different adapter retires
yours in the same transaction that installs the replacement, so its files
leave with the selection. `ggg remove` would refuse in either ordering:
while your adapter is still selected, removing it is a designed refusal
naming `ggg provider set`; once it is deselected, the lock no longer lists it.
Assert on the installed **files** rather than their directories — apply
deletes files and leaves the empty directories behind. The registry source
itself comes out with `ggg registry remove NAMESPACE`.

### 8. Tag

Commit `registry.json`, the indexes, the manifests, the payloads,
`registry.snapshot.json`, and `registry.snapshot.sig`, then tag a release:

```sh
git tag -a v1.0.0 -m "gadgetworks registry v1.0.0" && git push --tags
```

Consumers pin a ref, never a branch:

```sh
ggg registry add github:gadgetworks/ggg-registry \
  --namespace gadgetworks --ref v1.0.0 \
  --public-key ZKuGJnL3y0a1TwHe4Jax0T2pVD9eb7C0vEfgHAFGEsQ=
ggg provider set \
  --provider ggg/audit-export:development=gadgetworks/system/audit-export-ledger@ledger-file \
  --provider ggg/audit-export:test=gadgetworks/system/audit-export-ledger@ledger-file \
  --provider ggg/audit-export:production=gadgetworks/system/audit-export-ledger@ledger-cloud
```

`registry add` previews the namespace, the key fingerprint, the canonical
module, the modules the source brings, their dependencies, and the module
diff before writing anything.

## Versioning

`revision` moves on any implementation change. `contract` moves only when a
consumer has to change code — a changed capability type, a removed target, a
renamed environment key. Consumers declare the inclusive contract range they
accept (`"contract": {"min": 1, "max": 1}`), and the resolver refuses an
out-of-range dependency before it reads a single payload byte, so a breaking
change cannot land silently.

## Key rotation

Rotation is a published record, never an in-place swap: consumers pin the old
key, so a new key has to arrive under a signature they already trust.

```sh
ggg registry rotate --dir . \
  --old-key-file registry-private-key.b64 \
  --new-key-file registry-private-key-next.b64 \
  --not-before 2026-04-01T00:00:00Z
```

This writes `registry-key-rotation.json`
(`{namespace, old_fingerprint, new_public_key, not_before}`, RFC3339 UTC)
plus `registry.snapshot.old.sig` and `registry.snapshot.new.sig`. A consumer
honors the declared new key only after **both** signatures verify — the old
one under the key they pinned, the new one under the key being declared — and
only once their wall clock reaches `not_before`. Date `not_before` in the
future so everyone pins the new key before it activates, and republish a
snapshot without the rotation record once they have.

## Refusals you should expect

Every one of these refuses **before** any file is written:

| Situation | Result |
|---|---|
| Payload edited after signing | `registry snapshot payload … digest mismatch` |
| Signature replaced or truncated | `registry snapshot signature verification failed` |
| A file under `registry/` missing from the snapshot | `registry snapshot has unlisted payload …` |
| Consumer pins a namespace your `registry.json` does not declare | `registry namespace … does not match requested namespace …` |
| Two registries claiming one canonical module (or a prefix of it) | `canonical module … is claimed by registries …` |
| Two registries publishing the same scoped id | `duplicate scoped module id …` |
| A dependency outside the declared contract range | `contract …` |
| Two modules claiming one installed target path | `target namespace …` |
| A remote registry with no signature | `signed registry snapshot is required` |

An unsigned registry is consumable only as an explicitly configured
project-relative `directory` source. Remote registries must be signed.
