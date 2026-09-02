### D. Put core and external fixture `ggg registry validate` jobs in CI

Binding plan requirements (verbatim, task 10 and the final repository gate):

> Put core and external fixture `ggg registry validate` jobs in CI with separate stable work
> directories, and fill every manifest's `tests.e2e`/capability declarations so installed-module
> verification is enforced on each change.

> **Final repository gate:** `make check`, `make e2e`, `make visual`, `make smoke`,
> `make docker-build`, plus isolated core/external `ggg registry validate` CI jobs pass.
> Baselines change only via `make visual-update`.

Current state: `.github/workflows/ci.yml` (owned by `ggg/system/ci-github`) has five jobs —
`test`, `e2e`, `visual`, `smoke`, `docker`. `ggg registry validate` runs nowhere in CI, even
though it is the only gate that proves a module can be installed, compiled, tested, removed and
restored byte-for-byte. It now exercises 11 closures: the five original examples, the mail and
storage provider fixtures, the three `create resource` shapes, and the external-registry
template. Locally it takes ~90-120s and needs no database.

The `tests.e2e` half of the bullet was completed by task B: 34 specs across 24 owning modules,
enforced by `internal/modkit/e2e_ownership_test.go`.

#### What to build

1. **Two jobs, isolated from each other.** Add to `.github/workflows/ci.yml`:
   - a **core** validate job that exercises the closures living in this repository's
     `registry/testdata`, and
   - an **external** validate job that exercises `templates/external-registry` — the signed
     third-party template, including its signature verification and derivative install/remove.

   They must not share a work directory: the plan says "separate stable work directories", and
   the validator writes derivative trees. Read `internal/modkit/example.go` to find where the
   derivative root comes from and whether it is already parameterised; if the two jobs cannot be
   separated without splitting the command, either add the narrowest possible flag to select
   which closure family to exercise (and declare it in `internal/gggcli/table.go`) or run them
   in separate checkouts — pick the smaller change and justify it in your report.

2. **Real failure signal.** Each job must fail the build when a closure fails: check the exit
   code, and keep the validator's diagnostics in the log. Do not swallow output through a pipe
   that hides the status.

3. **Job wiring consistent with the existing file.** Follow the conventions already in
   `ci.yml`: `needs: test` where appropriate, `actions/checkout@v7`, `actions/setup-go@v7` with
   `go-version: "1.26.6"` and `cache: true`, `make setup` before any `ggg` invocation. No
   Postgres service unless the closures actually need one (they do not today — verify).

4. **Ownership.** `.github/workflows/ci.yml` is owned by `ggg/system/ci-github`
   (`registry/modules/system/ci-github/module.json`), so after editing it run
   `go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline` as one command.

5. **Docs.** `content/docs/testing.md` lists the gates; add the two jobs there so the documented
   gate set matches CI. Keep it accurate — no aspirational text.

#### Acceptance

- Both jobs exist in `.github/workflows/ci.yml`, with separate work directories, and each is
  demonstrated to work: run the exact command each job runs, locally, and paste the output in
  your report (including the closure count each one exercises).
- A test asserts the CI file keeps both jobs — the same style as the guard
  `internal/modkit/external_template_test.go` uses for the template's own workflow: the core and
  external validate invocations must be present, and neither may be reduced to a no-op. Put it
  where the other CI-shape assertions live if one exists; say where you put it and why.
- `go test ./internal/modkit ./internal/gggcli -count=1` passes.
- `go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline && go run ./cmd/ggg sync --check --offline` clean.
- Do NOT run `make check`, e2e, visual, or any project-wide formatter/linter. Do NOT push or
  trigger CI; the parent runs the final gate and the real CI run.
