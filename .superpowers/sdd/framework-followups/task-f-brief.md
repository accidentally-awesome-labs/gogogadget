### F. Close the runtime and CLI contract gaps the docs recast exposed

Task E could not write true documentation for six surfaces because the code does not do what the
plan and `AGENTS.md` say it does. Each item below is verified, not reported second-hand.

## F1 (Critical) — panic recovery is not installed

`internal/web/server.go:225-228` documents the chain as
`maxBytes → recover → routeBodyLimit → requestID → …`, `AGENTS.md` states the same order as
load-bearing, and `(*Server).recover` exists at `internal/web/middleware.go:59` — but
`Handler()` (`server.go:229-246`) never applies it, and a repo-wide grep for `s.recover(`
returns no callers. Panics therefore escape into `net/http`: the connection dies, no 500 page
renders, and `observability.Reporter` never captures the panic (`sentry.go`'s `PanicError` path
is unreachable from HTTP).

Fix: apply `recover` in the documented position (immediately inside `maxBytes`, outside
`routeBodyLimit`). Add a test that a handler which panics yields a 500 through the real
`Handler()` chain and that the reporter saw it — assert against the seam, not the SDK.

## F2 (Important) — `Runtime.Health` has no consumer, so `/readyz` ignores critical slots

The generated `Runtime.Health` (`internal/modules/bootstrap_registry_gen.go:968`) has zero
callers outside tests. `handleReadyz` (`internal/web/server.go:283-295`) is still a bare
`db.Ping`. The plan's contract: *"A provider outage blocks `/readyz` only for slots whose seam
declaration is `critical:true`; every other failure appears as degraded health and remains
actionable through `ggg doctor --runtime`."* Critical slots today are `ggg/database`,
`ggg/identity`, `ggg/billing`.

Fix: `/readyz` must consult the runtime health report — unhealthy `critical:true` slot ⇒ 503
naming the slot; unhealthy non-critical slot ⇒ 200 with the slot reported as degraded. Keep the
2s bound and keep it cheap enough for a probe. Tests: critical failure ⇒ 503, non-critical
failure ⇒ 200 + degraded, all healthy ⇒ 200.

## F3 (Important) — `ggg deploy apply` refuses even with `--yes`

`driveRemoteMutation` is called with `needsConfirm` unconditionally true for apply
(`internal/gggcli/remote_handlers.go:255-268`), and the confirmation path at `:61` refuses when
stdin is not a TTY or `--json` is set. The plan: *"Every remote mutation previews/confirms;
noninteractive requires `--yes`, and `--json` without it refuses."* So `--yes` MUST satisfy the
confirmation in a non-TTY/`--json` run; only its absence refuses.

Fix: thread the parsed `--yes` through so it satisfies confirmation, keep the refusal when it is
absent, and cover both directions plus exit codes.

## F4 (Important) — `ggg deploy plan` is an alias of `status`

`remote_handlers.go:255` and `:257` both dispatch `DeployStatusRequest`, so the change set is
only ever computed inside `apply`. The plan requires `DeployTarget.Plan(...) (DeployPlan, error)`
with `PlanHash`, `ObservedStateHash` and ordered `RemoteChange`s, feeding preview/dry-run, and
`apply` refusing `remote_plan_stale` when the observed hash moved. A `plan` that returns status
cannot preview a change set and cannot be the object `--resume RUN_ID` reloads.

Fix: dispatch a real plan request that calls `DeployTarget.Plan`, render the ordered changes
(paths only — `provider://…` / `deploy://…` grammar, never values), and keep `status` as the
observation command. Assert the two commands now produce different envelopes and that a stale
observed hash still refuses at apply.

## F5 (Minor) — `provider list`, `provider test` and `deploy status` print nothing in human mode

Their results carry payload-only data and `renderHuman` renders envelope fields only, so a
human-mode invocation prints an empty result. Fix: render the payload for these three (the JSON
envelope stays exactly as it is), and assert non-empty human output.

## F6 (Minor) — `make fuzz` runs one of two fuzz targets

`Makefile:49-50` runs only `FuzzFakeVerifier`; `FuzzSanitizeFilename` is never invoked by any
gate. Fix so both run in the same target, keeping the total fuzz time bounded.

## Out of scope — record only

- `internal/identity` and `internal/billing` still import the Clerk SDK, svix and
  standard-webhooks. `AGENTS.md` sanctions exactly one SDK file per seam, so this is
  as-designed; the *declaration* asymmetry (seam manifests declare those Go dependencies while
  the adapters declare none) is a follow-up, not this task.
- `ggg doctor --fix` implements one typed remediation (`env_file_missing`). The plan permits
  exactly "the remediation attached to a typed finding", so this is legal but thin. Record.

## Acceptance

- Each of F1-F6 fixed with a test that fails before the fix and passes after; say so per item in
  the report, naming the mutation you used to prove it.
- `go test ./internal/web ./internal/gggcli ./internal/modules ./internal/modkit -count=1` passes
  (`TEST_DATABASE_URL` is set in the environment; Postgres is up on localhost:5432).
- `go vet` and `gofmt` clean on touched packages.
- `go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline && go run ./cmd/ggg sync --check --offline` clean.
- The docs pages task E just rewrote must end up TRUE for these surfaces. If a doc sentence still
  disagrees after your fix, correct that sentence in the same change and list it.
- Do NOT run `make check`, e2e or visual; the parent runs the final gate.
