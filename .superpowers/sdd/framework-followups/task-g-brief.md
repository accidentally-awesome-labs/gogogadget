### G. Make a stale `bin/ggg` refuse instead of misreporting

Observed while running the final repository gate on this branch:

```
$ make check
  error    command_failed runtime module "ggg/system/server" has no provider for capability "runtime.health"
error: bin/ggg sync --offline: exit status 3
error: bin/ggg generate: exit status 1
make: *** [check] Error 1
```

Nothing was wrong with the tree. `bin/ggg` was a binary built *before* task F added the
`runtime.health` capability provider, while the manifests it read were the new ones. Rebuilding
`bin/ggg` and re-running the identical command succeeds. Two defects made that possible.

## G1 — `make check` does not rebuild a stale `bin/ggg`

`Makefile:5-6` declares `$(GGG): go build -o $(GGG) ./cmd/ggg` as a **file** target with no
prerequisites, so once `bin/ggg` exists make never rebuilds it, and every `bin/ggg`-driven target
(`check`, `generate`, `dev`, `test`, `db`, `services`, `build`, `setup`) silently runs a stale
engine against fresh manifests.

Fix: give `bin/ggg` its real prerequisites — the Go sources it is built from (`cmd/ggg` plus the
packages it compiles in, at minimum `internal/gggcli` and `internal/modkit`, excluding `_test.go`)
— so any engine change rebuilds it before the next target runs. Keep it boring: no phony rebuild
on every invocation, no timestamp hacks. `Makefile` is owned by `ggg/system/project-base`.

## G2 — the plan's engine-contract refusal does not exist

Plan task 8, verbatim: *"`setup` installs verified tools to `bin/<name>`, builds `cmd/ggg` to
`bin/ggg`, then uses that binary; **later commands refuse when the lock's CLI contract is
newer**."* There is no such refusal in the tree: the lock has no contract field
(`gogogadget.lock.json` keys are `dependencies, go_tools, modules, order, providers, registries,
registry_commit, runtime_orders, schema, snapshots`), and no code compares a compiled-in engine
version against anything. The failure above is exactly what the bullet was written to prevent,
and a consumer hits it the moment they update the registry without rebuilding their CLI.

Fix:
- Record the engine contract in the lock when it is written (one integer, defined as a single
  exported constant in `internal/modkit` beside the existing schema constant — do not invent a
  second versioning vocabulary; if `Lock.Schema` is genuinely the right carrier, say so in the
  report and use it rather than adding a field).
- On every command that reads the lock, refuse when the lock's recorded engine contract is newer
  than the running binary's, with an actionable message naming the remedy (`ggg setup`, or
  `go build -o bin/ggg ./cmd/ggg`) and the two versions. Exit code 3 (refusal), and it must fire
  before any write.
- A binary NEWER than the lock is fine and must not warn: that is the normal upgrade order
  (rebuild, then sync).
- Bump the constant in this change, since task F's `runtime.health` capability is exactly the
  class of engine change that makes an older binary wrong, and add the bump discipline to the
  docs page that documents the CLI contract.

## Acceptance

- `make check` from a tree whose `bin/ggg` predates an engine change rebuilds it and succeeds.
  Prove it: `touch internal/modkit/model.go && make -n check | head` shows the rebuild, and a
  real run passes.
- Tests: a lock recording a newer engine contract than the binary refuses with exit 3 and the
  actionable message, before any write; an equal contract proceeds; an older lock contract
  proceeds without a warning. Prove each fails before the fix (name the mutation).
- The refusal message is reachable from the real CLI, not only from a unit test.
- `go test ./internal/modkit ./internal/gggcli -count=1` passes; `go vet` and `gofmt` clean on
  touched packages.
- `go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline && go run ./cmd/ggg sync --check --offline` clean.
- Docs: the page that documents the CLI/engine contract states the guard and the bump rule. No
  aspirational text.
- Do NOT run `make check`'s siblings (e2e, visual, docker) — the parent runs the final gate.
