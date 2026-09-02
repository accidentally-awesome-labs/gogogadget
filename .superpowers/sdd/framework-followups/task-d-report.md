# Task D — core and external `ggg registry validate` jobs in CI

Implementation commit: `412cb44`. This report is the commit on top of it.

## What landed

| File | Change |
|---|---|
| `.github/workflows/ci.yml` | two new jobs, `registry-core` and `registry-external` |
| `internal/modkit/example.go` | `ClosureFamily` + `ParseClosureFamily`, `closuresForFamily`, family-keyed `exampleWorkDir`, `coreExampleClosures` split out of `ValidateExamples` |
| `internal/gggcli/{types,handlers,table,executors}.go` | `RegistryReadRequest.Closures`, `--closures` parsing and declaration |
| `internal/modkit/ci_workflow_test.go` | new guard: `TestCIExercisesEveryClosureFamilyForReal` |
| `internal/modkit/example_test.go` | three unit tests: flag vocabulary, empty-family refusal, work-directory separation |
| `content/docs/testing.md` | CI section rewritten: five jobs → seven, with what each validate job proves |
| `content/docs/cli.md`, `content/docs/modules.md` | the `--closures` flag and the two-family split |
| `registry/modules/system/{modkit,ci-github,content-assets}/module.json` | refreshed digests; `ci_workflow_test.go` declared by `ggg/system/modkit` (`class: "test"`) |

Everything else in the diff is generated churn, and it is churn only in the
`index:`/`registry:` lock-identity header lines — verified mechanically:

```
$ git diff -U0 -- internal/modules internal/web internal/i18n internal/db internal/api \
    static content/embed_registry_gen.go internal/config internal/gggcli/commands \
  | grep -E '^[+-]' | grep -vE '^(\+\+\+|---)' \
  | grep -vE '^[+-](//|#|/\*) (index|registry): [0-9a-f]{64}( \*/)?$'
(no output)
```

## The job definitions

```yaml
  registry-core:
    needs: test
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version: "1.26.6"
          cache: true
      - name: Setup
        run: make setup
      - name: Validate core closures
        run: bin/ggg registry validate --closures core

  registry-external:
    needs: test
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version: "1.26.6"
          cache: true
      - name: Setup
        run: make setup
      - name: Validate external registry template closures
        run: bin/ggg registry validate --closures external
```

Conventions match the existing file: `needs: test`, `actions/checkout@v7`,
`actions/setup-go@v7` with `go-version: "1.26.6"` and `cache: true`, `make
setup` before any `ggg` invocation. **No Postgres service**, verified rather
than assumed: the harness only ever runs `go tool templ`, `go tool sqlc`, `go
build ./...` and `go test` inside the derivative
(`internal/modkit/example.go`, `runGoTool` call sites), and both local runs
below passed with no database running.

**Real failure signal.** Each validate step is a bare command — no pipe, no
`|| true`, no `set +e`, no `continue-on-error`, no `if:` — so the step fails on
the validator's exit code and its diagnostics are the log. The refusal path is
exercised below (`--closures bogus` → exit 2), and the guard test fails the
build if anyone reintroduces a status-hiding wrapper.

## Work-directory isolation: what I decided and why

The plan requires "separate stable work directories". Two independent
mechanisms now guarantee it:

1. **Separate jobs → separate runners → separate checkouts.** That alone
   satisfies GitHub-side isolation.
2. **The scratch path is keyed by project root *and* family.**
   `exampleWorkDir(root, family)` is now
   `$TMPDIR/ggg-registry-validate-<8-byte-root-digest>-<family>`. Observed
   live during the concurrent run below:
   `/var/folders/.../T/ggg-registry-validate-9381658a80a28230-core`.

Relying on runner topology alone would have been a lie one `if: matrix` away:
the two commands are also run locally and in any single-machine CI, where they
previously shared one derivative *and* one `validate.lock`, so the second run
was refused outright by the pid lock. The one-line family suffix makes the
guarantee a property of the harness rather than of the runner layout, and it
also keeps each family's directory-keyed Go build cache warm instead of the two
families evicting each other. Cost: two derivative trees on disk instead of
one, which is the price of the isolation the plan asks for.

Proof both families now run concurrently against one checkout (impossible
before this change — the second run hit `registry validate is already running
against this project as pid N`):

```
$ (bin/ggg registry validate --closures core   > /tmp/val-core.log 2>&1;     echo "core EXIT=$?")     &
  (bin/ggg registry validate --closures external > /tmp/val-external.log 2>&1; echo "external EXIT=$?") &
  sleep 25; ls -d "$TMPDIR"ggg-registry-validate-*; wait
/var/folders/gc/mmsf5hkj517207_c2kskth9r0000gn/T/ggg-registry-validate-9381658a80a28230-core
external EXIT=0
core EXIT=0
```

(The external run had already finished and cleaned up by the 25s mark, so only
the core directory is listed; `TestClosureFamiliesGetSeparateWorkDirectories`
pins the distinctness deterministically.)

## Did I add a flag, and why

Yes: `ggg registry validate [--closures core|external|all]`, declared in
`internal/gggcli/table.go` (so it appears in derived `help` and completions)
and parsed in `runRegistry`.

The brief offered the alternative of "run them in separate checkouts" with no
flag. That does not produce the two jobs the plan names: without a selector,
both jobs would run the identical 11-closure command, so `registry-core` and
`registry-external` would be two labels for the same work — ~4.5 minutes of
duplicated compute per push, and a failure in the signed template would fail
both jobs while a fixture failure would also fail both. The plan's own
acceptance ("the closure count each one exercises") presumes different scopes.

The flag is the smaller change measured where it counts, and it is narrow by
construction:

- one optional string flag with a default that preserves today's behavior
  (empty ⇒ `all`; a bare `ggg registry validate` still exercises all 11
  closures);
- one exported enum plus one parser in `modkit`, so the workflow and the engine
  share a vocabulary the guard test checks by calling `ParseClosureFamily` on
  the command line it reads out of the YAML;
- `selectableClosureFamilies` is the single enumeration, so adding a third
  family fails `TestCIExercisesEveryClosureFamilyForReal` until a CI job covers
  it;
- no new command, no new envelope key, no change to exit semantics.

One behavior change beyond selection: **a named family that covers nothing is
now a refusal.** `all` keeps the accommodating answer (a derivative that
vendored the fixtures away must still be able to run the command), but
`--closures external` in a tree with no template exits non-zero instead of
printing nothing and exiting 0. Without that, deleting
`templates/external-registry` would have turned `registry-external` into a
permanently green no-op — exactly what the brief forbids.

## Local runs of the exact job commands

`make setup` first, as both jobs do:

```
$ make setup
go build -o bin/ggg ./cmd/ggg
bin/ggg setup
registry 94eb1cdba3800c7e5281d06d04011dd3602fbd16449e152a2cc5e62c4c18d30c
  update    lock       gogogadget.lock.json
(✓) Complete [ updates=0 duration=231.622208ms ]
≈ tailwindcss v4.3.3
Done in 91ms
```

### `registry-core` — 10 closures, exit 0, 136s

```
$ bin/ggg registry validate --closures core; echo "EXIT=$?"
preparing derivative from /Users/salar/Projects/gogogadget/.worktrees/framework-followups

ggg/element/example-token
  closure: ggg/element/example-token
  installed 3 file(s)
  compiled ./... and generated 26 file(s)
  module tests passed in ./internal/web/templates/ui
  removed; 1894 tree entries restored, 25 aggregate(s) differ only in the lock-identity header, 0 migration(s) retained

ggg/component/example-callout
  closure: ggg/element/example-token, ggg/component/example-callout
  installed 7 file(s)
  compiled ./... and generated 28 file(s)
  module tests passed in ./internal/web/templates/ui
  removed; 1894 tree entries restored, 25 aggregate(s) differ only in the lock-identity header, 0 migration(s) retained

ggg/page/example-status
  closure: ggg/element/example-token, ggg/component/example-callout, ggg/page/example-status
  installed 9 file(s)
  compiled ./... and generated 30 file(s)
  module tests passed in ./internal/web/templates/ui
  removed; 1894 tree entries restored, 25 aggregate(s) differ only in the lock-identity header, 0 migration(s) retained

ggg/workflow/example-feed
  closure: ggg/workflow/example-feed
  installed 3 file(s)
  compiled ./... and generated 28 file(s)
  removed; 1894 tree entries restored, 25 aggregate(s) differ only in the lock-identity header, 1 migration(s) retained

ggg/workflow/example-notice
  closure: ggg/workflow/example-notice
  installed 5 file(s)
  compiled ./... and generated 30 file(s)
  module tests passed in ./internal/web
  removed; 1894 tree entries restored, 25 aggregate(s) differ only in the lock-identity header, 1 migration(s) retained

ggg/workflow/example-ping
  closure: ggg/element/example-token, ggg/component/example-callout, ggg/page/example-status, ggg/workflow/example-ping
  installed 13 file(s)
  compiled ./... and generated 33 file(s)
  module tests passed in ./internal/web/templates/ui
  removed; 1894 tree entries restored, 25 aggregate(s) differ only in the lock-identity header, 1 migration(s) retained

ggg/workflow/example-resource
  closure: ggg/workflow/example-resource
  installed 5 file(s)
  compiled ./... and generated 30 file(s)
  module tests passed in ./internal/web
  removed; 1894 tree entries restored, 25 aggregate(s) differ only in the lock-identity header, 1 migration(s) retained

fixture/system/mail-providers
  closure: fixture/system/mail-local, fixture/system/mail-managed, fixture/system/mail-providers
  installed 4 file(s)
  compiled ./... and generated 29 file(s)
  module tests passed in ./internal/fixture/maillocal, ./internal/fixture/mailmanaged
  removed; 1894 tree entries restored, 25 aggregate(s) differ only in the lock-identity header, 0 migration(s) retained

fixture/system/storage-providers
  closure: fixture/system/storage-local, fixture/system/storage-managed, fixture/system/storage-providers
  installed 4 file(s)
  compiled ./... and generated 29 file(s)
  module tests passed in ./internal/fixture/storagefilesystem, ./internal/fixture/storages3
  removed; 1894 tree entries restored, 25 aggregate(s) differ only in the lock-identity header, 0 migration(s) retained

ggg/system/example-clock
  closure: ggg/system/example-clock
  installed 2 file(s)
  compiled ./... and generated 28 file(s)
  module tests passed in ./internal/example/clock
  removed; 1894 tree entries restored, 25 aggregate(s) differ only in the lock-identity header, 0 migration(s) retained
  info     example_closure_verified element closure ggg/element/example-token: installed 3 file(s), regenerated 26, compiled, 1894 tree entries restored byte for byte
  info     example_closure_verified component closure ggg/element/example-token+ggg/component/example-callout: installed 7 file(s), regenerated 28, compiled, 1894 tree entries restored byte for byte
  info     example_closure_verified page closure ggg/element/example-token+ggg/component/example-callout+ggg/page/example-status: installed 9 file(s), regenerated 30, compiled, 1894 tree entries restored byte for byte
  info     example_closure_verified workflow closure ggg/workflow/example-feed: … retained migration(s) internal/db/migrations/0027_ggg_example_feed.sql
  info     example_closure_verified workflow closure ggg/workflow/example-notice: … retained migration(s) internal/db/migrations/0027_ggg_example_notice.sql
  info     example_closure_verified workflow closure …+ggg/workflow/example-ping: … retained migration(s) internal/db/migrations/0027_example_ping_events.sql
  info     example_closure_verified workflow closure ggg/workflow/example-resource: … retained migration(s) internal/db/migrations/0027_ggg_example_resource.sql
  info     example_closure_verified system closure fixture/system/mail-local+fixture/system/mail-managed+fixture/system/mail-providers: installed 4 file(s), regenerated 29, compiled, 1894 tree entries restored byte for byte
  info     example_closure_verified system closure fixture/system/storage-local+fixture/system/storage-managed+fixture/system/storage-providers: installed 4 file(s), regenerated 29, compiled, 1894 tree entries restored byte for byte
  info     example_closure_verified system closure ggg/system/example-clock: installed 2 file(s), regenerated 28, compiled, 1894 tree entries restored byte for byte
EXIT=0
```

### `registry-external` — 1 closure, exit 0, 16s

```
$ bin/ggg registry validate --closures external; echo "EXIT=$?"
preparing derivative from /Users/salar/Projects/gogogadget/.worktrees/framework-followups

gadgetworks/system/audit-export-ledger
  closure: gadgetworks/system/audit-export-ledger
  registry gadgetworks verified at snapshot fa788a44733b94f6b6942662eb0ce4d43efe279097153a7add99fca275ade988
  installed 4 file(s)
  compiled ./... and generated 30 file(s)
  module tests passed in ./internal/gadgetworks/ledger
  removed; 1894 tree entries restored, 25 aggregate(s) differ only in the lock-identity header, 0 migration(s) retained
  info     example_closure_verified system closure gadgetworks/system/audit-export-ledger: installed 4 file(s), regenerated 30, compiled, 1894 tree entries restored byte for byte, published by registry gadgetworks at signed snapshot fa788a44733b94f6b6942662eb0ce4d43efe279097153a7add99fca275ade988
EXIT=0
```

10 + 1 = the 11 closures the brief names, now split across two jobs with no
overlap and no closure left unexercised.

### Refusals

```
$ bin/ggg registry validate --closures bogus; echo "EXIT=$?"
error: unknown closure family "bogus"; want core, external, all
EXIT=2

$ bin/ggg help registry | sed -n '7,10p'
Flags:
  --json
      emit the machine envelope
  --closures CLOSURES
      closure family to exercise: core, external, or all (validate)
```

## The guard test: where and why

`internal/modkit/ci_workflow_test.go`, package `modkit`, one test:
`TestCIExercisesEveryClosureFamilyForReal`.

There was no existing home for assertions about *this repository's* CI shape.
The nearest precedent, `assertTemplateWorkflowIsRunnable` in
`internal/modkit/external_template_test.go`, guards a different subject — the
workflow shipped inside `templates/external-registry`, which is a module
payload a publisher copies. Folding this repository's own gate into that file
would have put two subjects under one name. A new file in the same package is
where the guard belongs instead, because `modkit` is where the validator, the
closure families and `selectableClosureFamilies` live: the test can ask the
same function the command asks (`closuresForFamily`) rather than re-describing
the fixtures, and it can call `ParseClosureFamily` on the command line it reads
out of the YAML so the workflow and the engine cannot drift into two
vocabularies. It follows the style of the neighbouring repo-file guards
(`ownership_test.go`, `e2e_ownership_test.go`) and reuses their `specRepoRoot`
helper.

It parses `ci.yml` with `gopkg.in/yaml.v3` (already a direct dependency, used
in `modkit/compose.go`) rather than substring-matching, so it can see structure:
job-level and step-level `if:`/`continue-on-error`, `needs`, the pinned action
versions, step order.

Per selectable family it asserts: a job exists; it needs `test`; neither the
job nor the validate step is gated or continue-on-error; it checks out at
`actions/checkout@v7` and pins the same Go version the `test` job pins with
`cache: true`; it runs `make setup` *before* validating; it invokes `registry
validate` exactly once; the command is a bare `bin/ggg`/`go run ./cmd/ggg`
invocation with no `| ; & > ` or `set +e`; the `--closures` value parses through
`ParseClosureFamily` and is the expected family and never `all`; and the family
still resolves to at least one closure in this repository.

Mutation-tested — every degradation the acceptance criteria name is caught:

```
job deleted                -> FAIL (guard caught it)
family dropped             -> FAIL (guard caught it)
continue-on-error          -> FAIL (guard caught it)
gated by if                -> FAIL (guard caught it)
both families in one job   -> FAIL (guard caught it)
setup dropped              -> FAIL (guard caught it)
`|| true` appended         -> FAIL: job registry-core wraps the validator in shell
                                    that can hide its exit status
```

`ci.yml` was restored byte-for-byte after the mutation run (`git diff --stat`
showed only the intended 53-line addition).

Three supporting unit tests in `internal/modkit/example_test.go`:
`TestClosureFamilyParsing` (vocabulary, including that `CORE`,
`core,external` and `none` are refused rather than silently narrowed),
`TestNamedClosureFamilyRefusesWhenItCoversNothing` (a named empty family errors,
`all` does not), `TestClosureFamiliesGetSeparateWorkDirectories`.

## Verification

```
$ gofmt -l internal/modkit internal/gggcli        -> clean
$ go vet ./internal/modkit ./internal/gggcli      -> clean
$ go test ./internal/modkit ./internal/gggcli -count=1
ok  github.com/gogogadget/gogogadget/internal/modkit   8.358s
ok  github.com/gogogadget/gogogadget/internal/gggcli   0.590s

$ go test ./internal/modkit -run TestCIExercisesEveryClosureFamilyForReal -count=1 -v
    ci_workflow_test.go:81: registry-core exercises 10 closure(s) of family core
    ci_workflow_test.go:81: registry-external exercises 1 closure(s) of family external
--- PASS: TestCIExercisesEveryClosureFamilyForReal (0.22s)

$ go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline \
    && go run ./cmd/ggg sync --check --offline; echo "EXIT=$?"
registry 44930de81c04a225f7a21993939fdb7579091cf2f7277265519fef80f0060dac
registry 44930de81c04a225f7a21993939fdb7579091cf2f7277265519fef80f0060dac
EXIT=0
```

Not run, per the brief: `make check`, e2e, visual, project-wide formatters or
linters. Nothing pushed; no CI triggered.

## Concerns

1. **CI cost and wall time.** `registry-core` is ~136s locally on an M1 Max
   with a warm build cache; on a cold `ubuntu-latest` runner expect noticeably
   more, since each of the 10 closures compiles `./...` in a fresh derivative
   (the first closure pays the full build, the rest are warm because the
   derivative path is stable). `registry-external` is ~16s. Both are
   `needs: test`, so they do not delay the fast feedback. If the core job ever
   becomes the long pole, the honest fix is sharding the core family further
   (a third family and a third job, which the guard test would then require),
   not making the job optional.
2. **`bin/ggg` versus `go run ./cmd/ggg`.** The jobs use the binary `make
   setup` builds, which is what `ggg setup` exists to produce and what the
   Makefile targets use. The guard accepts either form, so a future switch to
   `go run` does not need a test edit.
3. **One-time `exit 4` during my own build/sync cycle.** Immediately after I
   edited `example_test.go`, the chained `registry build && sync --offline &&
   sync --check --offline` reported `1 pending change(s)` for
   `gogogadget.lock.json`; a second `sync --offline` settled it and every
   subsequent run of the full chain is clean and idempotent (shown above, run
   twice). I tried to reproduce it with a fresh payload-only digest change to
   `content/docs/testing.md` and it settled in a single pass, so I could not
   pin it down. Flagging it rather than dropping it: if the final gate ever
   sees a lone lock-update pending straight after a `registry build`, one more
   `sync --offline` is the workaround, and the root cause is in the lock write
   path, not in this slice.
4. **The external family is one closure.** `registry-external` proves the
   signed third-party path end to end, but it is a single adapter closure, so
   its coverage is as broad as the template is. Widening it is the template's
   slice, not this one — and the guard test will keep the job honest as the
   template grows.
