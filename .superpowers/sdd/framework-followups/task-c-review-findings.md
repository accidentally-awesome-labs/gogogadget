# Task C review findings — fix round 1

Source: ReviewRegistryTemplate of f1a5819..HEAD. Spec compliance FAIL, task quality WARN.
Confirmed strong by the reviewer: the `providerFixtureSpec` → `candidates` refactor is
provably behaviour-preserving, the external closure runs the full lifecycle through the
production `VerifyRegistrySnapshot` path, `testRun` widens rather than loosens, all four tamper
refusals are write-free by construction, ownership is real without widening `projectOwned`, all
nine reference facts are present, the demonstration key cannot be mistaken for a secret, and
every command in `extending.md` exists. Fix the items below.

## Critical

- **C1 — the template's CI workflow cannot run**, which falsifies the one deliverable whose
  entire purpose is being copied by a third party
  (`templates/external-registry/.github/workflows/registry.yml`):
  - `:60-64` `ggg new … --non-interactive --json` passes no `--registry`, and
    `internal/gggcli/task8_handlers.go:102-107` refuses with
    `noninteractive new requires registry answers`.
  - `:69` `ggg registry add "directory:$GITHUB_WORKSPACE"` — `$GITHUB_WORKSPACE` is absolute and
    `internal/modkit/validate.go:101-104` refuses with `path must be project-contained`.
  - `:83` `ggg registry validate` inside the scratch consumer is an always-green no-op:
    `ValidateExamples` finds neither `<root>/registry/testdata` nor
    `<root>/templates/external-registry` (`internal/modkit/example.go:205-210`, `:515-518`) and
    returns nothing. A step labelled "Validate the lifecycle" that exercises nothing is worse
    than no step.
  Fix all three as the reviewer sketched: give `ggg new` a `--registry`; copy the registry INTO
  the consumer and add it with a relative `directory:` path; and either drop the validate step
  or move it where it has closures to exercise.

- **C2 — tighten the guard so C1 cannot recur.** Extend the workflow assertion in
  `TestExternalRegistryTemplateSignatureIsReproducible`
  (`internal/modkit/external_template_test.go:428-437`) from substring presence to: `ggg new`
  carries `--registry`, and no `directory:` argument in the workflow begins with `/` or `$`.

## Important

- **I1 — the exemplar adapter selects its target by credential presence**, the exact
  anti-pattern plan step 2 ordered deleted and step 3 removed from `internal/mail` and
  `internal/storage`. `ledger.go.txt:268-285`: with `ledger-cloud` selected and
  `GADGETWORKS_LEDGER_ENDPOINT` unset, `NewModule` returns the development file exporter instead
  of refusing — structurally `ResendConfigured()`. It also never demonstrates consuming the
  generated config struct (`Deps struct{}` + `host.Env`), while declaring `field` names that
  generate config fields nothing reads. Both "no fallback" claims (`ledger.go.txt:14-15` and the
  README) are false for the missing-endpoint case.
  Fix: `Deps{Config *config.Config}` like `internal/mail/resend/resend.go:14-27`, branch on the
  *selected target* rather than on credential presence, refuse a `ledger-cloud` selection with a
  missing endpoint by key name, turn
  `TestNewModulePrefersTheEndpointOverTheLocalFile` (`ledger_test.go.txt:151`) into a refusal
  test, and re-align the two prose claims.

- **I2 — the exemplar CLI handler imports a package that holds an `*http.Client`.**
  `cli.go.txt:20` imports the `ledger` package, and `ledger.go.txt:28` imports `net/http`
  (`:75,95`). `matchBannedImport` (`internal/modkit/cli_scan.go:88-100`) is direct-import-only,
  so the boundary scan passes and the exemplar teaches the shape that walks around it — for four
  constants. Fix: move `ModuleID`/`Slot`/`TargetFile`/`TargetCloud` into a constants-only package
  (or inline them), then extend `TestExternalRegistryTemplateHandlerRespectsTheCLIBoundary` to
  walk the handler's imports one hop within the template's own packages and fail on a banned
  import reached transitively. (The scan's general non-transitivity is pre-existing and NOT in
  scope — only the template's own graph is.)

## Minor

- **M1** — delete `exerciseProviderClosure` (`internal/modkit/example.go:969-975`): it takes a
  spec, discards it with `_ = spec`, and tail-calls `exerciseStandardClosure`, which re-derives
  the same spec four times. Call the callee directly from `exerciseExampleClosure:749-751`.
- **M2** — `providerChoicesFromModules:765-787` is last-write-wins across
  candidates × targets × environments. Add a one-line clash refusal so disjointness is enforced
  rather than incidental.
- **M3** — `content/docs/extending.md:251` opens the publisher sequence with `ggg registry init`
  right after telling the reader to copy the template, which already has `registry.json`. Add
  the clause naming the alternative (edit `namespace`/`canonical_module` in the copied file).
- **M4** — `templates/external-registry/README.md` step 7 tells a publisher that
  `ggg registry validate` proves their registry's lifecycle. It does not, outside this repo.
  State what the command actually exercises and what a publisher should run instead.

## Maintenance order to respect

The core manifest pins the template's snapshot and signature digests
(`registry/modules/system/registry-template/module.json:50,58`), so after editing template files
run: `ggg registry build --dir templates/external-registry` → `registry sign --dir …` →
`ggg registry build` → `ggg sync --offline`.
