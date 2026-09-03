# Task F report — the six runtime and CLI contract gaps

All six items implemented, each with a test that fails before the fix and passes
after. Commit `dce9e7e` on `framework-followups` (parent `57578ed`).

---

## F1 — panic recovery is now installed

**Changed:** `internal/web/server.go` — `Handler()` applies `s.recover(h)`
between `routeBodyLimit` and the telemetry span, i.e. inside the global body cap
and outside every named middleware. The doc comment now states the position and
why the two unnamed wrappers (provider environment, telemetry span) stay outside
it: the 500 page renders with the same chrome context every other page gets, and
the span closes on the recovered 500 rather than unwinding through a panic.
`recover` itself was already correct and needed no change.

**Test:** `internal/web/recover_readyz_test.go`
`TestPanicBecomesFiveHundredThroughHandlerChain` — registers a panicking handler
on the real mux, drives it through the real `Handler()` chain via the existing
`serve` helper, and asserts 500, the panic detail on the page (cfg.Env is
`test`, so not production), and exactly one `CaptureRequest` on the
`observability.Reporter` **seam** (`capturingReporter`, not the Sentry SDK).

**Proving mutation:** deleted the `h = s.recover(h)` line. The panic escapes
`Handler().ServeHTTP` and the test process dies on the panic itself:
`FAIL github.com/gogogadget/gogogadget/internal/web 0.564s`, stack through
`web.maxBytes.func1 → web.serve → TestPanicBecomesFiveHundredThroughHandlerChain`.
Restored → `ok`.

## F2 — `/readyz` consumes the runtime health report

**Changed:**
- `internal/apphost/apphost.go` — added `type HealthFunc func(context.Context) HealthReport`. It is a named type because the manifest `type` field must satisfy `validGoTypeRef`, which rejects a bare func literal type.
- `internal/modkit/generate.go` — added `runtimeProvidedCapabilities` (`runtime.health` → `apphost.HealthFunc(r.Health)`): the capabilities the generated `Runtime` supplies from itself. `collectRootNeeds` marks them provided so they never become an `Options` field (nothing outside `Boot` could produce one), and each boot branch seeds its provider map with the receiver expression.
- `internal/modkit/resolve.go` — the per-environment runtime DAG skips these needs: they create no edge and require no providing module. Without this, resolution refused with `runtime module "ggg/system/server" has no provider for capability "runtime.health"`.
- `registry/modules/system/server/module.json` — declares the need `{field: Health, capability: runtime.health, type: apphost.HealthFunc, optional: false}`; revision 1 → 2.
- `internal/web/server.go` — `Deps.Health`, `Server.health`, a `readyzResponse` body type, and a rewritten `handleReadyz`: db ping first (a pool that cannot answer makes every other check meaningless), then the report. `!report.Ready` → 503 `critical slot unhealthy` naming the critical slots; any unhealthy non-critical slot → 200 `degraded` naming them; otherwise 200 `ok`. Slot names only — no check message reaches a public probe body. Bounded at 2s, and `HealthCache` already caches for 10s.
- `internal/web/module.go` — `NewModule` refuses a nil `Health`. `NewModule` is called only by the generated bootstrap, so a broken wiring is a boot error; a `Server` built directly by a test keeps the db-ping-only path.
- `internal/web/module_test.go` — the servable-handler test now passes a `Health` func, and the missing-capability table gained a `health` case.

**Test:** `TestReadyzConsultsRuntimeHealth` (three cases: critical unhealthy ⇒
503 + `critical:["ggg/database"]`; non-critical unhealthy ⇒ 200 +
`degraded:["ggg/mail"]`; all healthy ⇒ 200 `ok`), each also asserting the check
messages (`pool closed`, `429 from provider`) do **not** appear in the body.
Plus `TestReadyzDegradesWithoutHealthCapability` for the nil case.

**Proving mutation:** replaced `report := s.health(ctx)` with
`apphost.HealthReport{Ready: true}` (the old "ignore the report" behavior). The
non-critical case fails with `expected: degraded / actual: ok`, and the critical
case fails the same way. Restored → `ok`.

## F3 — `deploy apply` accepts `--yes` noninteractively

**Changed:** `internal/gggcli/remote_handlers.go` — removed the special-case
apply branch that passed `needsConfirm: true` unconditionally. All three
mutating actions now share one gate: `confirmed := yes || mutation.Resume != ""`
feeds both `requireRemoteConfirm` and `needsConfirm`. `--resume` is included
because it replays a run whose plan was already confirmed, matching what
`provider provision` already did.

**Test:** `internal/gggcli/remote_contract_test.go`
`TestDeployApplyAcceptsYesNoninteractively` — four subtests over a scripted
`fakeDeployTarget`: `--yes` reaches `Apply` (non-TTY, since `runAppWith` uses
buffers), `--yes --json` reaches `Apply` and emits the `deploy apply` envelope,
and both `deploy apply` and `deploy apply --json` without `--yes` refuse with
exit 3 and an `applied` count of zero.

**Proving mutation:** re-inserted
`if action == "apply" { return driveRemoteMutation(..., true) }`. Both `--yes`
subtests fail with the confirmation error. Restored → `ok`.

**Real-binary smoke:** `/tmp/ggg-taskf deploy apply --environment production --json`
→ `error: deploy apply: noninteractive runs require --yes`, exit 3.
With `--yes` added it passes the gate and reaches the Fly target
(`flyctl: … Could not find App "gogogadget"`, exit 1) — a different failure, and
the one this environment should produce.

## F4 — `deploy plan` computes a real change set

**Changed:**
- `internal/gggcli/types.go` — new sealed `DeployPlanRequest{Environment}`.
- `internal/gggcli/controller.go` — `Execute` dispatches it.
- `internal/gggcli/remote_exec.go` — `executeDeployPlan` runs the same
  `previewDeploy` path `apply` does (read-only: `resolveDeployment`,
  `deployPrior`, `DeployTarget.Plan`) and reports `deploy_plan` with `module`,
  `target`, `environment`, `plan_hash`, `observed_state_hash` and the ordered
  changes. Its envelope `run_id` is `remoteRunID("deploy", planHash)` — the same
  id the apply persists, so it is the object `--resume RUN_ID` reloads.
  `remoteChangeEnvelope` also projects the changes onto the fixed envelope
  vocabulary with `class: remote`.
- `internal/gggcli/remote_handlers.go` — `plan` dispatches `DeployPlanRequest`;
  `status` still dispatches `DeployStatusRequest`.

**Test:** `TestDeployPlanComputesChangeSetAndIsNotStatus` — asserts the two
envelopes differ, `command` is `deploy plan` vs `deploy status`, `deploy_plan`
appears only in plan and `deployment` only in status, the plan and observed
hashes round-trip, the two ordered changes carry the `deploy://fake/app` and
`deploy://fake/secrets` paths in declared order, the envelope `changes` carry
`class: remote`, and `DATABASE_URL` appears as a key name with no value
(`postgres://` absent). `TestDeployApplyRefusesStalePlan` keeps the other half:
a target whose fresh `Status` observes `obs-2` against a plan confirmed at
`obs-1` refuses with `remote_plan_stale`, exit 3, and never applies.

**Proving mutation:** reverted the dispatch to `DeployStatusRequest`. Fails with
`expected: "deploy plan" / actual: "deploy status"`, identical envelopes, and a
`deployment` payload where none was expected. Restored → `ok`.

**Real-binary smoke:** `/tmp/ggg-taskf deploy plan --environment production` now
reaches flyctl (`Could not find App "gogogadget"`) instead of returning a status
envelope — it is calling `DeployTarget.Plan`.

## F5 — human rendering for the payload-shaped read-only remote commands

**Changed:** `internal/gggcli/app.go` routes `provider` and `deploy` to a new
`renderRemote` in `internal/gggcli/remote.go`, which switches on
`Envelope.Command` for `provider list`, `provider test`, `deploy plan` and
`deploy status` and falls through to `renderHuman` for everything else (so every
mutating provider/deploy command is untouched). Each renderer reads exactly the
payload keys the JSON envelope already carries, then still calls `renderHuman`
so envelope diagnostics and the failure line keep printing. The JSON shape is
unchanged.

**Test:** `TestReadOnlyRemoteCommandsRenderInHumanMode` — four subtests
asserting non-empty stdout plus the specific facts (slot, environment,
`adapter@target`, declared input key names, deployment module/target/readiness,
plan hashes and remote paths).

**Proving mutation:** removed the `case "provider", "deploy":` arm. `provider
list`, `provider test` and `deploy status` all fail with
`Should NOT be empty, but was`, and `deploy plan` prints only the two envelope
change rows without the module or hashes. Restored → `ok`.

**Real-binary smoke:**
```
$ /tmp/ggg-taskf provider list --slot ggg/mail
ggg/mail               development  ggg/system/mail-dev@filesystem  development/manual
ggg/mail               test         ggg/system/mail-dev@filesystem  development/manual
ggg/mail               production   ggg/system/mail-resend@resend  managed/manual
$ /tmp/ggg-taskf provider test --slot ggg/mail --environment development
ggg/mail development  ggg/system/mail-dev@filesystem
  automation manual
```

## F6 — `make fuzz` runs both fuzz targets

**Changed:** `Makefile` — `FUZZTIME ?= 8s` (per target, so the gate stays
bounded at roughly the previous single-target wall time) and the `fuzz` recipe
runs `FuzzFakeVerifier` (`./internal/identity/`) and `FuzzSanitizeFilename`
(`./internal/mail/dev/`). `registry/modules/system/project-base/module.json`
revision 1 → 2 (it owns the Makefile).

**Test:** `internal/modkit/fuzz_gate_test.go`
`TestFuzzGateInvokesEveryFuzzTarget` — scans every tracked/untracked `_test.go`
for `func FuzzXxx`, parses the Makefile's `fuzz` recipe, and fails naming any
target the recipe does not `-fuzz=`. The check is against the declared targets,
not a count, so a new fuzz target joins the gate or fails the suite.

**Proving mutation:** deleted the `FuzzSanitizeFilename` recipe line. Fails with
`these fuzz targets are declared but no gate invokes them:
[FuzzSanitizeFilename (internal/mail/dev/fuzz_test.go)]`. Restored → `ok`.

**Gate run** (`FUZZTIME=3s` to keep this report's run short; both targets fuzz):
```
$ FUZZTIME=3s make fuzz
fuzz: elapsed: 3s, execs: 366852 (122283/sec), new interesting: 0 (total: 12)
ok  	github.com/gogogadget/gogogadget/internal/identity	3.289s
fuzz: elapsed: 3s, execs: 189193 (63059/sec), new interesting: 0 (total: 49)
ok  	github.com/gogogadget/gogogadget/internal/mail/dev	3.370s
```

---

## Doc sentences corrected

Every one of these was true at `57578ed` and false after the fix. All are in the
same commit.

| Page | What changed |
|---|---|
| `content/docs/architecture.md` §"Request lifecycle" | The chain diagram gained `recover` between `telemetry.HTTP` and `routeBodyLimit`, plus a paragraph on why it sits outside every named middleware and inside the span/provider-environment wrappers. |
| `content/docs/architecture.md` §4 (generated bootstrap) | The health paragraph now names `/readyz` as the report's consumer and `runtime.health` as the one capability the Runtime supplies from itself. |
| `content/docs/deployment.md` §"/healthz vs /readyz" | Was "200 only when a database ping succeeds". Now: ping first, then the report, with a three-row status/body table (ok / degraded / critical), the "names slots, never messages" rule, and the 2s + 10s bounds. |
| `content/docs/deployment.md` §"Deploying" bullets | Was "`plan` and `status` both take the target's authoritative reading" and "`deploy apply` … refuses a noninteractive run". Split into a `plan` bullet (calls `Plan`, reports `deploy_plan`, path grammar, key names only, the resumable run id) and a `status` bullet, and one uniform confirmation bullet covering apply/rollback/secrets. |
| `content/docs/testing.md` §"Fuzz" | Was "runs `FuzzFakeVerifier` for 15s … the `fuzz` target does not currently invoke it". Now a two-row target table, `FUZZTIME`, and the gate-check test. |
| `content/docs/roadmap.md` §"Known gaps" | Deleted four rows: panic recovery not installed, runtime health has no HTTP consumer, `deploy apply` cannot run noninteractively, read-only remote commands print nothing, and `make fuzz` runs one of two targets. Replaced the `deploy plan` alias row with the declaration-asymmetry gap the brief told me to record. |
| `content/docs/roadmap.md` §"Shipped" | Health row now states the `/readyz` contract; Remote-operations row now states the plan/status split and the `--yes`/`--resume` confirmation rule. |

`content/docs/observability.md`'s claim that "the `recover` middleware captures
the exception … then renders the 500 page" needed no edit — F1 makes it true.
`AGENTS.md`'s stated middleware order needed no edit for the same reason.

## Commands run

```
$ go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline
registry d592e0aba4d2f5b52e579623eb2ab41d795650522216c42beafb141b0b49632b
  update    lock       gogogadget.lock.json

$ go run ./cmd/ggg sync --check --offline
registry d592e0aba4d2f5b52e579623eb2ab41d795650522216c42beafb141b0b49632b

$ gofmt -l internal/web internal/gggcli internal/modkit internal/apphost
(clean)

$ go vet ./internal/web ./internal/gggcli ./internal/modkit ./internal/apphost ./internal/modules
(clean)

$ go test ./internal/web ./internal/gggcli ./internal/modules ./internal/modkit -count=1
ok  	github.com/gogogadget/gogogadget/internal/web	72.950s
ok  	github.com/gogogadget/gogogadget/internal/gggcli	0.632s
ok  	github.com/gogogadget/gogogadget/internal/modules	2.574s
ok  	github.com/gogogadget/gogogadget/internal/modkit	8.238s

$ go test ./internal/apphost ./internal/config -count=1
ok  	github.com/gogogadget/gogogadget/internal/apphost	2.256s
ok  	github.com/gogogadget/gogogadget/internal/config	0.224s

$ go build ./...
(clean)

$ make -n fuzz
go test -run=^$ -fuzz=FuzzFakeVerifier -fuzztime=8s ./internal/identity/
go test -run=^$ -fuzz=FuzzSanitizeFilename -fuzztime=8s ./internal/mail/dev/
```

`make check`, e2e and visual were **not** run, per the brief — the parent runs
the final gate.

## Manifest and revision bookkeeping

Manifests whose owned source changed had their revisions bumped (contracts
unchanged): `ggg/system/server` 1→2, `ggg/system/apphost` 1→2,
`ggg/system/modkit` 8→9, `ggg/system/project-base` 1→2. The three new test files
are declared as `class: test` payloads on their owning modules
(`internal/web/recover_readyz_test.go` → server;
`internal/gggcli/remote_contract_test.go` and `internal/modkit/fuzz_gate_test.go`
→ modkit), so `TestEveryTrackedSourceFileHasAnOwner` stays green and a
derivative install gets them. `ggg/system/content-assets` took a digest refresh
only, matching what the docs-recast commit `fbf2526` did.

## Commit

- `dce9e7e` — `fix: close the six runtime and CLI contract gaps the docs recast exposed`

## Concerns

1. **`runtime.health` is a new kind of capability.** It is the first capability
   the `Runtime` provides from itself rather than from a module constructor.
   That required three small carve-outs (`collectRootNeeds`, the boot-branch
   provider seed, and the runtime-DAG edge skip), all keyed off one
   `runtimeProvidedCapabilities` map, so adding a second such capability is one
   map entry. But it *is* a concept the schema does not describe: no manifest
   declares `runtime.health` as a provide, and nothing in
   `registry/schema/module.schema.json` records that it exists. If this pattern
   grows past one entry it deserves an explicit declaration in the schema rather
   than a Go-side map. Worth a look from whoever owns the schema.

2. **`ggg deploy plan` now reaches the provider on a read-only command.**
   `Execute` is documented as "never writes to the project", which holds —
   `DeployTarget.Plan` performs no mutation and nothing is persisted. But `plan`
   now makes a network/flyctl call where before it made a (different) network
   call for status, so the cost profile is comparable and no worse. Flagging it
   only because "read-only request" now unambiguously means "may talk to the
   provider".

3. **`deploy plan`'s run id is derived, not persisted.** It equals the id an
   apply of the same plan hash would persist, so `--resume` lines up — but
   `plan` itself does not write a run record. That is deliberate (a preview
   writes nothing), and it means `--resume` still requires an apply to have
   started. If the intent is that `plan` alone should be resumable, that is a
   separate decision about whether previews persist.

4. **`/readyz` returns 200 for a degraded non-critical slot, by contract.** That
   is what the plan requires, but it means a platform health check cannot see
   provider degradation. The body reports it (`{"status":"degraded","degraded":[…]}`)
   and `ggg doctor --runtime` is the operator view; a monitor that wants to alert
   on degradation has to read the body, not the status code. Documented in
   `deployment.md`.

5. **Recorded, not fixed** (per the brief's out-of-scope section): the
   `internal/identity` / `internal/billing` vendor-SDK declaration asymmetry —
   the seam manifests declare the Clerk, svix and standard-webhooks Go
   dependencies their adapters should own — is now the roadmap's replacement gap
   row. `ggg doctor --fix` still implements exactly one typed remediation
   (`env_file_missing`), which the plan permits; its roadmap row is unchanged.
