# U. Make the gates deterministic — report

Repository `/Users/salar/Projects/gogogadget`, branch `main`, from `c63e66e`.
Host: macOS 27.0, Apple M1 Max, 10 logical cores. Postgres for the Go suite is
the project's own test stack container (`localhost:15432`); the operator's
Homebrew Postgres on 5432 was never touched, and every container this work
started was removed.

Summary of outcomes:

| # | Outcome |
|---|---|
| 1 | Already fixed upstream. Used as evidence for the class guard (item 5). |
| 2 | Already fixed upstream. Used as evidence for the class guard (item 5). |
| 3 | **Fixed.** Real product defect: the publish instant came from the application clock and the visibility predicate reads the database clock. Reproduced at a measured rate, fixed, re-verified against the same reproduction. |
| 4 | **Attribution refuted, no machinery added.** CPU load is measurably not the mechanism: the exact spec family that failed passes at 1.8× the incident's load average with 65% of its budget unused. Recorded note, named residual. |
| 5 | **Narrow scan landed with 0 measured false positives.** The broad version was measured at 93% false positives and refused with the numbers. |

One gate is **not** green: `make e2e` fails `export.spec.ts` at a measured 33%
per attempt. It is pre-existing, unreachable from this change, and diagnosed to
the point of "the object the `files` row names is not on disk" — see the gates
section. It is called out rather than absorbed, which is the whole point of
this ticket.

---

## Item 3 — `TestPublishedBodyEscapesMarkup` + `TestNewContentTypePublishesThroughGenericTemplates`

### The mechanism, at file:line

Two clocks decide one thing, and nothing orders them.

- **Write** — `internal/web/workflow_admin_content.go:177` (pre-fix):
  `publishedAt = pgtype.Timestamptz{Time: s.cfg.Now(), Valid: true}`.
  `Config.Now()` is `internal/config/config.go:212-217`; `testNow` is the zero
  value here because `integrationServer` builds `config.Config` as a struct
  literal (`internal/web/testhelpers_test.go:51-64`), so `Now()` returns
  `time.Now()` — the **application** clock.
- **Read** — `internal/db/queries/content_entries.sql:9` (`ListLiveEntries`),
  `:20` (`GetLiveEntry`), `:30` (`LatestLiveEntry`): `published_at <= now()`.
  `now()` is `transaction_timestamp()` — the **database** clock.

So an entry published with no date is live only once the database clock reaches
an instant taken from the application clock. If the database clock trails the
application clock by δ, the entry is invisible for δ, and both reported tests
publish and then read on the very next request:

- `internal/web/content_cms_test.go:123-129` — publish, then
  `GET /blog/cms-xss`, then assert the escaped body. A miss renders the 404
  page, so `assert.Contains(body, "raw HTML omitted")` fails.
- `internal/web/content_type_test.go:106-116` — publish, then `GET /guides`,
  then `GET /guides/deploying-to-fly`. A miss drops the entry from the index
  and 404s the detail page.

This is not a test artefact. It contradicts the product promise the suite
itself states — "publishing must be visible on the next request, not the next
TTL" (`internal/web/content_cms_test.go:47`) and
`/docs/content` — and it bites hardest exactly where a real deployment puts
the database on a different machine from the app.

### Measured skew and measured margin

The competition is `δ` (database clock behind host clock) versus the width of
one HTTP round trip. Both were measured on the project's own test stack.

`δ`, from a bounded round trip `t0 → clock_timestamp() → t1` (300 samples):

| host load average | δ p50 | δ max |
|---|---|---|
| ~12 (ambient) | 0.72 ms | 0.76 ms |
| ~19–21 | 2.42 ms | 2.56 ms |
| ~34–42 | 3.56 ms | **4.99 ms** |

The margin the real HTTP path leaves, measured with a temporary probe inside
`internal/web` that ran the exact publish-then-read sequence 40 times and read
back `now() - published_at`:

| host load average | margin min | margin p05 | margin p50 |
|---|---|---|---|
| ~34 | **2.37 ms** | 3.15 ms | 4.66 ms |
| ~42 | 10.08 ms | 13.00 ms | 19.87 ms |

**The distributions overlap.** Worst measured δ (4.99 ms) exceeds best measured
margin (2.37 ms). That is the whole flake: a low-probability crossing, not an
ordering coupling.

Stripped of the HTTP round trip, the same pair of statements — insert
`published_at = host time.Now()`, then select `published_at <= now()` — misses
outright:

| host load average | δ p50 | invisible immediately after publish |
|---|---|---|
| ~12 | 0.72 ms | **72/300 (24.0%)** |
| ~19–21 | 2.42 ms | 276–281/300 (92.0–93.7%) |
| ~34–42 | 3.56 ms | 211/300 (70.3%) |

And a single in-transaction reading of the pre-fix write, taken on the live
stack, puts the application stamp **2.438 ms ahead** of the transaction's own
`now()`.

### Reproduction, with a rate

`go test -race -count=25 -run '<the pair>' ./internal/web/` against the
project's own stack fails **0 of 25** times: at δ = 0.72 ms the margin is never
crossed, which is precisely why isolation and three consecutive sweeps could
not show it. Reproducing on demand needs a database whose clock trails by more
than one round trip, so the rig makes δ a dial:

```sh
# postgres:17 + faketime; the whole image lives in tmp/, nothing installed on the host
docker build -t ggg-skewpg:local tmp/flakehunt/skewpg

# a throwaway server whose clock trails the host by OFFSET seconds
docker run -d --name ggg-skewrate -e POSTGRES_PASSWORD=postgres -p 15435:5432 \
  --entrypoint faketime ggg-skewpg:local -f "-0.002" docker-entrypoint.sh postgres

TEST_DATABASE_URL="postgres://postgres:postgres@localhost:15435/postgres?sslmode=disable" \
  go test -count=10 -v ./internal/web/ \
  -run 'TestDraftIsInvisibleUntilPublished|TestPublishedBodyEscapesMarkup|TestNewContentTypePublishesThroughGenericTemplates'
```

Driven by `tmp/flakehunt/rate.sh <offsets…>` (`COUNT=10`), **before the fix**:

| faketime offset | δ measured (p50) | `…DraftIsInvisible…` | `…PublishedBodyEscapesMarkup` | `…NewContentTypePublishes…` |
|---|---|---|---|---|
| `0` | ~1 ms (image baseline) | 0/10 | 0/10 | 0/10 |
| `-0.002` | 2.956 ms | 7/10 | **6/10** | **10/10** |
| `-0.005` | 5.291 ms | 10/10 | **9/10** | **10/10** |
| `-0.010` | 10.224 ms | 10/10 | 9/10 | 10/10 |
| `-0.050` | 50.342 ms | 10/10 | 10/10 | 10/10 |
| `-1` | 1.0005 s | 10/10 | 10/10 | 10/10 |

The failure output is the reported symptom verbatim: `/blog/cms-xss` renders
"Page not found", and `/guides` renders without "Deploying to Fly".

The threshold sits between ~1 ms and ~3 ms of trail. **The project's own test
stack was measured at 0.72 ms idle and up to 4.99 ms under load** — it crosses
that threshold under load and sits below it when settled. That is the complete
account of why the pair failed once on a loaded machine and then passed in
isolation and on three subsequent sweeps.

### What the two tests actually share (and what they do not)

The brief asked for this to be established concretely rather than assumed.

- **The `cache.Store` is not shared, and not even present.** `integrationServer`
  never sets `Deps.Cache` (`internal/web/testhelpers_test.go:65-74`), so
  `content.NewCMSWithCache(..., d.Cache, ...)` at `internal/web/server.go:181`
  receives nil and `CMS.store` is nil (`internal/content/cms.go:104`). The only
  cache is the per-instance, mutex-protected map at `internal/content/cms.go:106-107`.
  (Worth recording for the future: `Invalidate()` at `internal/content/cms.go:205-212`
  expires only the local map and leaves a configured `store` untouched, so a
  *shared* store would be a real staleness bug — it just is not this one.)
- **The two tests do not land in the same database.** `integrationServer` calls
  `testdb.Open(t, "web")` (`internal/web/testhelpers_test.go:40-44`), and
  `testdb.DSN` **drops and recreates** `gogogadget_test_web` on every call
  (`internal/db/testdb/testdb.go:140-165`), then migrates it
  (`internal/db/testdb/testdb.go:55`). There are 240 `integrationServer` call
  sites in the package and each gets a virgin migrated database — confirmed by
  the goose log printed once per test. No row, slug, sequence or leftover
  fixture can carry from one `internal/web` test to another. `"web"` is also
  unique across the tree, so no other package touches that database.
- **Nothing package-level is mutated.** No `t.Parallel()` anywhere in
  `internal/web` (0 occurrences), so the package is strictly sequential and
  single-goroutine through `s.Handler().ServeHTTP` at
  `internal/web/testhelpers_test.go:155`. The goldmark instance at
  `internal/content/content.go:24` is shared but only ever entered from the
  test's own goroutine, and `Render` uses a fresh buffer
  (`internal/content/content.go:228-234`).
- **No frozen clock, and the TTL cannot interact with a slow sweep.**
  `TEST_NOW` is never exported for the Go suite, so `Config.Now()` is
  wall-clock. Every content mutation calls `s.cms.Invalidate()`
  (`internal/web/workflow_admin_content.go:187`), which zeroes `expires`, so the
  next read re-queries no matter how long the sweep takes. `cmsTTL` is
  irrelevant here.
- **A `t.Cleanup` delete cannot race a running handler** — it cannot even run:
  see the latent finding below.
- **What they do share, and uniquely.** They are two of exactly **three** tests
  in the package that publish an entry carrying **no** `published_at` and then
  assert it is live on the next request. The third is
  `TestDraftIsInvisibleUntilPublished` (`internal/web/content_cms_test.go:28`),
  and the rig fails it alongside them. Two of three tipping over is what a
  per-request coin flip looks like; it is not evidence of coupling between the
  two, which is why "what do these two share" only resolves once the shape is
  the unit rather than the pair.

### The fix

Stamp the publish instant from the clock that decides visibility, in the same
statement.

- `internal/db/queries/content_entries.sql:86-99` — new `PublishEntry`:
  `SET status = 'published', published_at = COALESCE(published_at, now()), updated_at = now()`.
  `COALESCE` keeps a date the editor supplied, which is what makes a future one
  scheduled rather than live, and the read-modify-write in the handler collapses
  into one statement. The codebase already had this pattern for the same reason
  (`internal/db/queries/jobs.sql:7`, `internal/db/queries/schedules.sql:5`); the
  publish path was the outlier.
- `internal/web/workflow_admin_content.go:166-189` — the handler calls
  `s.q.PublishEntry(ctx, existing.ID)`; the application clock is gone from the
  path. `handleAdminContentUnpublish` keeps `SetEntryStatus`, whose
  retain-the-existing-date semantics it needs.
- `internal/content/cms_test.go:194-249` — `TestPublishStampsTheDatabaseClock`
  pins the invariant **deterministically**, without needing the rig: `now()` is
  `transaction_timestamp()` and therefore constant for one transaction, so a
  database-stamped `published_at` is byte-identical to that transaction's
  `now()`, while an application-stamped one cannot be. Proven to discriminate:
  a throwaway run of the pre-fix shape in the same transaction put the stamp
  2.438 ms away from `now()` and the assertion failed on inequality.
- `content/docs/content.md:77-89` — the one-clock rule, with the measured
  numbers, in the page that documents the publishing predicate.

Every other `s.cfg.Now()` consumer was checked (19 call sites): all of them are
render-clock uses (relative-time formatting, plan-expiry comparison in Go) or
the date-derived slug at `internal/web/workflow_admin_content.go:429`. Line 177
was the only place an application-clock instant was persisted into a column the
database then compares against its own clock, which is why the fix is one query
and not a sweep. (`billing.CurrentPlanWithCatalog(..., s.cfg.Now(), ...)` and
`internal/db/queries/orgs.sql:30` do read the same value against two clocks, but
the value is a 30-day subscription boundary, so the crossing window is
microseconds wide once a month rather than every publish. Recorded, not
changed.)

### Verification against the reproduction

Same rig, same command, `COUNT=10`, **after the fix**:

| faketime offset | δ measured (p50) | `…DraftIsInvisible…` | `…PublishedBodyEscapesMarkup` | `…NewContentTypePublishes…` |
|---|---|---|---|---|
| `-0.002` | 3.22 ms | 0/10 | 0/10 | 0/10 |
| `-0.005` | 5.423 ms | 0/10 | 0/10 | 0/10 |
| `-0.050` | 50.391 ms | 0/10 | 0/10 | 0/10 |
| `-1` | 1.000445 s | 0/10 | 0/10 | 0/10 |

A one-second trail — six hundred times the worst δ this machine produces — no
longer moves the tests, because there is no longer a second clock to disagree
with.

**Status: fixed.** Not quarantined, no timeout raised, no retry added.

### Latent finding, reported not fixed

While establishing what the two tests share: **13 `t.Cleanup` closures in
`internal/web` call `t.Context()`**, which Go cancels *before* cleanup
functions run, so every one of those statements is a silent no-op with its
error discarded. Two of them are in the reported pair
(`internal/web/content_cms_test.go:121`, `internal/web/content_type_test.go:98`),
which is why the pair looks like it cleans up after itself and does not. The
full list:

```
internal/web/account_data_test.go:163
internal/web/admin_content_editor_test.go:50, :123
internal/web/content_cms_test.go:121, :217, :281
internal/web/content_type_test.go:98
internal/web/dunning_test.go:54, :78, :94, :120
internal/web/media_test.go:94
internal/web/roles_test.go:73
```

`seedEntries` gets this right (`context.Background()`,
`internal/web/testhelpers_test.go:105`). This is **not** the cause of item 3 and
has no observable effect today, because `testdb` drops the database per test —
which is exactly what makes it dead code that lies about its purpose rather
than a bug. Left as a named follow-up: it spans eight modules' payloads for
zero behavioural change, and folding it into this change would put unrelated
revision bumps in the same commit.

---

## Item 4 — the `a11y-states` timeouts

### Concurrency, measured rather than assumed

| fact | value | source |
|---|---|---|
| Playwright `workers` | **5**, not pinned in config — the default `ceil(cores/2)` on 10 logical cores | `npx playwright test --list --reporter=json` → `config.workers` |
| projects | **2**: `chromium` (557 tests), `mobile` (113, `testMatch` scoped to `a11y-states.spec.ts`) | `e2e/playwright.config.ts:43-57` |
| total tests | 670 in 34 files; the e2e gate excludes `visual.spec.ts` (200 chromium tests) → ~470, which matches the reported 467 | `--list` |
| `fullyParallel` | true | `e2e/playwright.config.ts:35` |
| retries | 0 locally, 2 in CI | `e2e/playwright.config.ts:36` |
| per-test budget | interaction cases: `30_000 × (scans ?? 1) + (awaitsVendor ? 50_000 : 0)`; scenario cases: the 30 s default | `e2e/a11y-states.spec.ts:433` |
| interaction cases | 18; 2 declare `scans > 1`, 1 declares `awaitsVendor` | `e2e/a11y-states.spec.ts:112-418` |
| e2e stack containers | **2** (`app`, postgres) + 1 volume | `compose.test.yaml` |
| containers on the host | **26** running, **3** in a restart loop | `docker ps` |
| suite's own CPU draw | `user 9m32s / real 2m40s` = **3.6 of 10 cores** | `time npx playwright test a11y-states.spec.ts` |

The "25 containers" in the incident report were **ambient**, not the suite's:
the e2e stack is two containers, and this machine independently sits at 26 with
three supabase containers crash-looping for other projects.

### Headroom, measured against the real budget not the default

`a11y-states.spec.ts` already carries per-case budgets, so "timed out at 30s"
localises the failures to the 16 `scans: 1` interaction cases whose budget is
exactly 30 000 ms. Budget utilisation, full 226-test file:

| condition | interaction worst | `tab-panels` (90 s budget) | scenario worst (30 s budget) | result | wall |
|---|---|---|---|---|---|
| ambient, loadavg ~16 | **32.8%** (9 834 ms) | 30.8% (27 680 ms) | 10.3% (3 086 ms) | 224 passed, 2 skipped | 2 m 40 s |
| loadavg ~28 | p99 20 651 ms, max 25 701 ms | — | — | 224 passed, 2 skipped | 2 m 45 s |
| **loadavg 143.5** (16 CPU spinners + the 26 ambient containers) | **35.0%** (10 506 ms) | 29.0% (26 089 ms) | — | 70 passed, 2 skipped (interaction subset) | 2 m 32 s |

### Verdict

**The stated attribution does not survive measurement.** I ran the exact spec
family that failed — the interaction states — at **loadavg 143.5, 1.8× the
incident's 81.8**, on the same 10-core machine with the same 26 containers up.
Every test passed, and the tightest one used 35.0% of its budget: it had 65%
spare. Going from loadavg 16 to 143 moved worst-case utilisation by six
percentage points, and wall time went *down* slightly (2 m 32 s vs 2 m 45 s).
macOS time-slices pure CPU demand away from the browsers; it does not stretch
them 3× as the reported timeouts require.

The incident's real signature is the **5.7× wall-clock inflation** (16.6 min
against 2.9 min). CPU load reproduced 0.9×. Whatever slowed that run was not
CPU demand.

So, on the three options the brief named:

- **A worker cap: not supported.** The suite draws 3.6 of 10 cores; it is not
  the contended party. Dropping to 3 workers would cost roughly 50% of wall
  time on every good run to recover a fraction of an 8×-oversubscription
  deficit. That is a permanent tax against an unreproduced condition.
- **A load guard that refuses above a threshold: refuted, not merely
  unjustified.** A guard keyed on load average would have refused the run I
  just measured passing with two thirds of its budget unused. That trades a
  false failure for a false refusal, which is worse: the false failure at least
  points at a test.
- **Nothing beyond a recorded note: this is the answer.** Recorded here and in
  `content/docs/testing.md`.

**Named residual, rather than a closed book.** The one component independently
measured misbehaving on that machine that day is the Docker Desktop VM: item 3
measured its Postgres clock trail growing from 0.72 ms to 4.99 ms as host load
rose, and the e2e suite's server *and* database both sit behind that VM. That
is a lead, not a conclusion — I did not reproduce a 5.7× inflation from it. What
would change this verdict is a reproduction that inflates e2e wall time ~5×;
CPU saturation demonstrably does not, so the next attempt should target VM I/O
and memory pressure (many containers churning), not `loadavg`.

`retries: 2` in CI was reviewed and left alone: Playwright reports a
retry-passed test as `flaky` rather than `passed`, so it labels the condition
instead of hiding it, and a deterministic failure still fails all three
attempts.

**Status: no defect found in the suite; attribution refuted with numbers; note
recorded.** Neither fixed nor quarantined, because measurement says there is
nothing in the harness to fix or skip.

---

## Item 5 — a guard for the class

Items 1 and 2 were the same defect: a test reading state a server goroutine
writes, with no synchronisation. Two candidate scans were written and **both
measured over all 289 `*_test.go` files in the tree** before either was
considered for landing.

### R1 — narrow: an `httptest.ResponseRecorder` crossing a `go` statement

**0 hits. 0 false positives.**

`httptest.ResponseRecorder` has no synchronisation of any kind, and a streaming
handler never stops writing it — which is why item 1's fix was to replace the
recorder (`internal/web/realtime_test.go:16-53`) rather than to move the read.
The rule is the *handoff*, not the read, deliberately: a safe read requires
proving a happens-before edge to every write, and that is exactly the reasoning
that was got wrong. A recorder that never crosses a `go` statement needs no
such proof.

Landed as `modkit.ValidateNoRecorderGoroutineHandoff`
(`internal/modkit/test_scan.go`), wired into the plan at
`internal/modkit/plan.go:354` alongside the other nine scans, so it runs on
every `sync`, `check`, `add` and `registry validate` — in derivative projects
too, over their own installed test payloads. It selects files by the `_test.go`
suffix rather than the declared class, because 8 of the 281 `_test.go` payloads
in this tree are declared `class: "go"` and a class-keyed rule would leave
exactly those unscanned. It resolves the import alias, so `ht "net/http/httptest"`
does not evade it, and it catches both `httptest.NewRecorder()` and a
`httptest.ResponseRecorder` composite literal, addressed or not.

Unit tests at `internal/modkit/test_scan_test.go` cover the incident verbatim,
a direct `go serve(rec, …)` call, the composite-literal spelling, the aliased
import, a same-named local that is *not* the guarded type, the sanctioned
mutex-guarded recorder on a goroutine (allowed), a recorder used on the test's
own goroutine in a function that also starts an unrelated one (allowed), and a
non-test payload (ignored). The whole-tree assertion is not duplicated as a
unit test: the scan runs on every plan, so `bin/ggg sync --check --offline`
passing *is* the standing statement that the tree is clean.

### R2 — broad: a local written on a goroutine/handler closure and read outside it

**30 hits, 28 false positives — a 93% false-positive rate. Refused.**

This is the rule that would generalise to item 2 (`upstreamBytes`, a plain
`int`). It does catch it. It also catches the ordinary `httptest.NewServer`
fixture whose handler has returned before the client call does — a shape
`go test -race` proves clean on every single run, and there are 28 of them:

```
internal/analytics/analytics_test.go:15, :16, :17
internal/billing/polar/client_test.go:31
internal/jobs/webhooks_deliver_test.go:131, :221, :222, :223, :224, :361, :362 ×2
internal/mail/smtp/starttls_test.go:57, :58, :67 ×2
internal/modkit/source_test.go:268, :288, :307
internal/storage/s3/protocol_test.go:19, :20, :22
internal/web/ingest_posthog_test.go:35, :36
internal/web/notifications_test.go:126
internal/web/route_test.go:244 ×2, :301
```

The two true positives it found are both **already fixed** code that it flags
anyway — `internal/web/ingest_posthog_test.go:147` and
`internal/modkit/source_test.go:184`, distinguished only by the heuristic note
"write is inside a Lock-bearing closure". Refining it further is escape
analysis, which is `-race`'s job and which `-race` already does soundly: it is
what caught items 1 and 2. A static rule at 93% false positives would train
people to work around it.

**Refused with evidence, per the brief's explicit allowance.** `-race`
remains the guard for the general class; R1 removes its sharpest instance from
the space of writable code.

Both measurements are reproducible; the rig is its own Go module, so the
project's `go test ./...` never sweeps it:

```sh
cd tmp/flakehunt/scan && go run . /Users/salar/Projects/gogogadget
```

Re-run after this change (290 files, the two new ones included): R1 **0 hits**,
R2 30 hits — unchanged.

---

## Gates

| gate | result |
|---|---|
| `go test -race` — `internal/modkit` | ok, 56.1 s |
| `go test -race` — `internal/content` | ok, 4.8 s |
| `go test -race` — `internal/db/...` | ok |
| `go test -race` — `internal/web/...` | ok |
| `make check` | pass |
| `bin/ggg registry validate` | pass |
| `make e2e` | **466 passed, 202 skipped, 1 failed** — `export.spec.ts` only. Pre-existing, unrelated to this change, diagnosed below. |
| `make visual` | **not run** — no template, token or component was touched |

### A fifth flake, found by running the gate: `export.spec.ts`

`make e2e` reported `1 failed — [chromium] › export.spec.ts:7:5 › export
produces a downloadable CSV` and `1 flaky — notifications.spec.ts`. Measured
rate, serially, `--workers=1 --retries=0`, on a settled machine (loadavg 13):
**4 of 12 runs fail** — a 33% per-attempt failure. (An earlier
`--repeat-each=10` measurement of 6/10 is discarded: `fullyParallel` runs the
repeats concurrently as the same org, which the real suite never does.)

It is not reachable from this change. The diff touches the content publish
query and handler, `internal/modkit`'s new scan, `internal/content/cms_test.go`,
two docs pages, and the manifests/generated outputs that follow. The failing
path is the jobs worker, the filesystem storage store and
`internal/web/workflow_files.go` — none of which this change enters. With
`retries: 2` (this shell exports `CI=true`, so the config's CI branch applies)
the suite-level failure probability is ~3.6%, which is why it has been passing
and why it read as noise.

Evidence chain, as far as it got:

1. The error is `download.createReadStream: canceled` at
   `e2e/export.spec.ts:25`.
2. The trace's download event names `url: /app/files/1` with
   `suggestedFilename: "1.html"` — the browser downloaded an **HTML page**, not
   the CSV. A `Content-Disposition` was therefore never set, so
   `DevStore.Serve` returned before `internal/storage/filesystem/dev.go:64`,
   which leaves only the `os.Open` failure at `:55-57`.
3. That error reaches `internal/web/workflow_files.go:114-116`, which calls
   `renderError` (`internal/web/htmx.go:235-238`) — 500 plus an HTML page. The
   anchor carries `download` (`internal/web/templates/files.templ:98`), so
   Chromium downloads that page as `1.html` and then cancels it.
4. Confirmed on disk: after a failing run the `files` row exists
   (`id 1`, key `exports/org_pro/1788628154935978093-projects-20260905-170914.csv`,
   `size_bytes 207`) and **the object at that key does not**, while 148 objects
   from earlier runs sit in the same directory.

So `exportProjectsCSV` (`internal/jobs/export_csv.go:47-54`) reported a
successful `Put` with a byte count, inserted the row with the same `key`
variable, and the bytes are absent. No caller deletes it: the only
`Storage.Delete` sites are the two rollbacks and the explicit row/media
deletes, none of which ran. **I did not close this**, and I am not guessing at
a fix for a storage-durability defect on the strength of a locator.

What the next pass should do: run `cmd/server` under its own supervision with
`E2E_NO_WEBSERVER=1` (the harness is set up for it) and read the server log
across a failing run — `Put`'s reported size against the file's actual state,
and whether `os.Create`/`io.Copy`/the deferred `Close` at
`internal/storage/filesystem/dev.go:42-47` is discarding an error. `Put`
returns `io.Copy`'s result *before* the deferred `Close` runs, so a failing
`Close` — a full or unsynced filesystem — is reported as a successful write
with a byte count. That is the first thing to instrument, and it is the same
defect class as the rest of this ticket: a success reported from the wrong
observation.

Core release order run in full, since manifest-owned payloads changed:

```sh
bin/ggg registry build
bin/ggg registry sign --dir . --key-file ~/.config/ggg/core-registry.key
bin/ggg sync --offline
bin/ggg sync --check --offline
```

Revisions bumped for every module whose payloads changed:

| module | revision |
|---|---|
| `ggg/system/content` | 1 → 3 |
| `ggg/system/modkit` | 29 → 31 |
| `ggg/system/content-assets` | 8 → 9 |
| `ggg/workflow/admin-content` | 3 → 4 |

## Environment left behind

- Every container this work started (`ggg-skewpg`, `ggg-skewpg2`, `ggg-skewrate`,
  `ggg-skewbase`) removed; the `ggg-skewpg:local` image and the `tmp/flakehunt/`
  rig are kept because they are the reproduction, and `tmp/` is ignored.
- The operator's Homebrew Postgres (PID-owned, port 5432) was never contacted:
  `testdb.BaseDSN` derives the test stack's 15432, and every rig container got
  its own port.
- The project's own test stack (`compose.test.yaml`) was already up and is left
  up. No volumes created or deleted.
- The temporary probe test in `internal/web` was deleted; its measurements are
  recorded above.

---

# Fix report — review of `41293fb`

Verdict was spec WARN / quality **FAIL** on one finding, and the finding was
right: **I fixed the write and left the read.** Everything below is the
response, in the reviewer's numbering.

## I1 (blocking) — the badge still answered to the application clock

`41293fb` moved the publish instant to the database clock and left
`/admin/content`'s badge computing liveness from `Config.Now()`. Under
`APP_ENV=test` that clock is frozen at `TEST_NOW=2026-01-15`, so a
database-stamped `published_at` is months later and
`e.PublishedAt.Time.After(now)` was true: a live entry rendered **"Scheduled"**.
In production it is the app-behind-database direction — the mirror of item 3,
and the direction the faketime rig had never run.

Fixed the read the way the write was fixed: one clock, and liveness stopped
being a timestamp comparison in a template.

- `internal/db/queries/content_entries.sql` — `ListEntriesAdmin` returns a
  `lifecycle` column: `CASE WHEN status <> 'published' THEN 'draft' WHEN
  unpublish_at IS NOT NULL AND unpublish_at <= now() THEN 'expired' WHEN
  published_at IS NOT NULL AND published_at > now() THEN 'scheduled' ELSE
  'live' END`. That is a literal transcription of `contentStatus`'s four
  branches in their original order, evaluated against the same `now()` as the
  live predicate.
- `internal/web/templates/admin_content.templ` — `contentStatus` and
  `contentNow` are gone, `Items` is `[]sqlc.ListEntriesAdminRow`, the badge
  renders `e.Lifecycle`, and `ContentListData.Now` is removed because the badge
  was its only consumer (the date cell formats `e.PublishedAt` directly).
- `internal/web/page_admin_content.go` — no longer passes `Now: s.cfg.Now`.

**Verified in both directions**, `COUNT=8`, four tests each
(`tmp/flakehunt/rate.sh`):

| faketime offset | measured db offset | failures |
|---|---|---|
| `-1` | db 1.0014 s behind host | 0/8 × 4 |
| `-0.005` | db 6.41 ms behind host | 0/8 × 4 |
| `+0.005` | db 4.14 ms **ahead** of host | 0/8 × 4 |
| `+1` | db 999 ms **ahead** of host | 0/8 × 4 |

And proved the new assertion **discriminates** rather than merely passing. A
throwaway test against a database 1 s ahead of the host published a dateless
entry and compared both computations on the same row:

```
published_at            = 2026-09-05 17:34:47.829609 +0000 UTC
render clock cfg.Now()  = 2026-09-05 17:34:46.835453 +0000 UTC
pre-I1 template badge   = scheduled
SQL lifecycle column    = live
```

The pre-I1 comparison calls a live entry scheduled; the SQL column calls it
live. Throwaway deleted.

Both doc sentences the review named are true again:

- `content/docs/content.md` — the "computed badge … cannot drift from what the
  public site does" paragraph now says *why* it cannot: it is computed by the
  listing query off the same `now()`, and the frozen-clock failure is named.
- `content/docs/testing.md` — the `TEST_NOW` determinism paragraph now bounds
  itself: "That clock **formats**; it never decides state."

**The `mediaNow` sibling, answered.** It is formatting only, and correct as it
stands. Its single caller is `RelTime(mediaNow(d), m.CreatedAt.Time)` — a
relative time over `content_media.created_at`. No liveness, schedule or expiry
is decided from it, so freezing it at `TEST_NOW` is exactly the determinism the
baselines want. Said so in a comment on the function so the next reader does
not have to re-derive it.

## I2 — the branch had no gate, and now has two

The only publish in the suite (`e2e/admin-content.spec.ts:50`) fills an
explicit past date first, with a comment saying it does so to keep the badge
deterministic. So it takes `PublishEntry`'s keep-the-date branch and never the
`COALESCE` one. `make e2e` passing 466 was never evidence about `41293fb`, and
`make visual` proved nothing either.

Added `e2e/admin-content.spec.ts` › *"post: a publish with no date is Live in
the admin and live on the site"*: no `content-published-at` (asserted empty),
save, assert Draft, publish, assert the badge is **Live**, then load
`/blog/<slug>` and assert the heading. It ran and passed in the final gate
(`admin-content.spec.ts:148`, 3.5 s).

## I3 — a handler-level assertion, plus the caveat stated rather than worked around

`internal/web/content_cms_test.go` › `TestDatelessPublishIsLiveEverywhereItIsReported`
drives `POST /admin/content` then `POST /admin/content/{id}/publish` with no
date, and asserts both halves: the entry is live on `/blog` and
`/blog/<slug>` **and** `/admin/content` renders "Live" and not "Scheduled".

The caveat, stated because it bounds the guarantee: there is no deterministic
*value*-level discriminator at the handler layer. `integrationServer` builds
`config.Config` as a literal, so `testNow` is unreachable and `cfg.Now()` is
plain wall clock — an application stamp and a database stamp are identical on
any machine whose clocks agree, which is every CI machine. **I did not wire
`testNow` into the integration server**; the frozen clock's job is visual
determinism and giving it a second job is how the badge got into this state.
So this test earns its keep against a clock-offset database, in both
directions, which is how it was verified;
`TestPublishStampsTheDatabaseClock` remains the deterministic discriminator via
`transaction_timestamp()`.

## No scan, and the reason is the interesting part

Considered and rejected on the parent's guidance, recorded because the reason
generalises: **the defect is already unrepresentable at the site that held it.**
`PublishEntry(ctx, id)` takes an id and nothing else, so a handler physically
cannot supply a `published_at` for a publish. Removing the parameter is a
stronger guard than any scan or test over it, because there is no longer a
wrong value to pass. That is the strongest form of this move and worth naming
as a pattern: prefer deleting the parameter to checking what arrives in it.
The second reason is arithmetic — a scan for `cfg.Now()` flowing into a
`timestamptz` write would be the same over-broad shape refused at 93% one
commit ago, and this tree has legitimate application-clock writes.

## M1 — eight mis-declared payloads corrected, and the selector was wrong twice

Reclassified `class: "go"` → `"test"` on the eight `_test.go` payloads that
were mis-declared: `internal/api/{cursor,tokens}_test.go` and
`internal/content/{changelog,cms,content,docs_check,search,types}_test.go`.
`ggg/system/api` was bumped for this even though `registry build` did not
demand it — no payload bytes changed, only declarations, and a manifest that
changed at a standing revision is the same lie the revision rule exists to
prevent.

Confirmed the reclassification does not exempt them from a scan that should see
them. `ValidateConfigFieldOwnership` iterates payload targets by `.go` suffix,
not by class, so it is unaffected. `ValidateAssetReferences` already skipped
both the class and the suffix. `cli_scan` skips only generated files.
`ValidateNoCredentialPresenceSelectors` **was** scanning those eight as product
code by accident and now correctly skips them — its own comment says a test may
name a provider and its keys deliberately. To keep a future mis-declaration
from putting a test back under a product-code rule, that predicate now checks
the `_test.go` suffix alongside the class.

Then the selector on the new scan itself, which was wrong in both directions
before it was right:

- Suffix only (as landed in `41293fb`) missed the eight payloads above.
- Class only (my first fix) handed the Go parser **235 non-Go payloads** that
  are legitimately `class: "test"` — 200 committed PNG baselines, 34 `.ts`
  specs and `internal/gggcli/testdata/new-saas.json`. It broke every plan, and
  the error's "(and 1 more errors)" made a tree-wide failure read as two files.
  Caught by `StorageDurability`, who measured the real scope instead of
  trusting the message.
- The correct selector is the conjunction: `Class == FileClassTest && strings.HasSuffix(target, ".go")`.

Verified three ways: eight unit cases, a new
`TestRecorderHandoffIgnoresNonGoTestPayloads` pinning the JSON/spec/PNG skip,
and a throwaway run over the real lock and the real bytes on disk — 266 Go test
payloads scanned, 235 non-Go skipped, nothing refused, with an assertion that
`new-saas.json` was actually in the input set so the skip is not vacuous. And
live, on every `sync`/`sync --check` in this commit.

## M2 — the doc comment now states its bound

`ValidateNoRecorderGoroutineHandoff`'s comment says what it does **not** see:
recorders returned by a helper (`internal/web/idempotency_test.go` builds them
that way), handoffs through `errgroup.Go` or any function that starts a
goroutine for its caller, and the general class, which remains `-race`'s
business. "It removes one shape from the space of writable code; it does not
decide the question."

## M3 — the measured refutation is in the repository, not only in this report

The item-4 numbers now live in `content/docs/testing.md` under **"When a spec
times out, measure before you cap anything"**: the per-case budget formula,
32.8% of budget at load average 16, 35.0% at load average 143, the wall clock
not moving (2m32s against 2m40s), the suite's 3.6-of-10-core draw, and the
instruction to look at the shared substrate rather than re-run. That paragraph
is committed in `b87fdcd`.

This matters more than the pointer being tidy. `.superpowers/sdd/.gitignore`
is `*`; this report is tracked only because it was force-added, and sibling
reports are not tracked at all. So "it is in the report" is not a durable
record. Anything a future reader needs about the product belongs in
`content/docs/`.

## M4 — the R2 locations are enumerated in this report

All 30 hits of the rejected broad rule, with file and line, are listed in the
item-5 section above, so the 93% claim can be spot-checked from source without
the rig. The rig itself lives under gitignored `tmp/flakehunt/` and is its own
Go module so `go test ./...` never sweeps it.

## Visual baselines: four rewritten, and why that is the fix rather than the symptom

`make visual` failed 4 of 200 — `admin-content` × {light,dark} ×
{mobile,tablet}, 7962 pixels, ratio 0.03 against a 0.01 tolerance. Diagnosed
before touching anything: queried the fixture's 11 content entries and computed
both badges side by side in SQL.

| | badge under db `now()` | badge under frozen `TEST_NOW` | rows |
|---|---|---|---|
| | `live` | `scheduled` | **10** |
| | `live` | `live` | 1 |

**The committed baselines encoded I1 in its original direction**: ten of eleven
rows badged "Scheduled" for blog posts dated 2026-01-22 and 01-29 and eight
changelog entries dated 2026-08-02 through 08-22 — content `/blog` and
`/changelog` have been serving the whole time, because the arbitrating clock
was frozen in January. A baseline is the record of what correct looks like, and
this one had been recording a wrong state word since the frozen clock was
introduced. Updated with `make visual-update`, the sanctioned writer, on the
parent's explicit go-ahead. The four files:

```
e2e/visual.spec.ts-snapshots/admin-content-dark-desktop-chromium-linux.png
e2e/visual.spec.ts-snapshots/admin-content-dark-mobile-chromium-linux.png
e2e/visual.spec.ts-snapshots/admin-content-light-desktop-chromium-linux.png
e2e/visual.spec.ts-snapshots/admin-content-light-mobile-chromium-linux.png
```

Rejected alternative: re-date the fixture so the badges match the old
baselines. That means moving real blog and changelog dates into the future to
satisfy a screenshot, and it keeps a baseline whose meaning decays with the wall
clock — the defect class being removed.

**A tolerance lesson worth recording.** The pre-update run failed at *mobile and
tablet* and passed at *desktop*; the post-update diff is at *desktop and
mobile* and tablet came out byte-identical. Desktop had changed all along — the
badge column is a smaller fraction of a wider image, so its diff slipped under
`maxDiffPixelRatio: 0.01`. A ratio threshold hides a real difference at some
viewports and not others, so "which breakpoints failed" is not the same
question as "what changed".

## New fixture: the Scheduled and Expired states, pinned decay-proof

The imported corpus is all past-dated, so it only ever exercises "live" — the
baseline was getting "Scheduled" by accident from the frozen clock, and after
the fix would have covered neither Scheduled nor Expired. Added
`internal/db/testdata/seed/e2e/content.sql` (declared `class: "seed"` on
`ggg/system/content`) with two rows.

The dates are chosen to be **decay-proof, not merely distant**, because a
"far future" date that arrives breaks the baseline for no code reason — the same
defect on a longer fuse:

- Scheduled: `published_at = 9999-12-31`. Wall clocks only move forward, and
  this is at the far end of the representable calendar, so it can never be
  reached.
- Expired: `published_at = 1970-01-01`, `unpublish_at = 1970-01-02`. Already
  past, and time moving forward can only keep it past — it can never recede
  into the future. (Ordered to satisfy
  `content_entries_expiry_after_publish`.)

Neither can flip under any clock, in any timezone, for the life of this
repository. "Draft" stays covered at the e2e layer, where the new spec asserts
it on a row it creates before publishing.

## Gates on the combined tree

Run on my slice on top of `b87fdcd`, after the stash handoff (all 12 of my
files verified restored, independently of the handoff's own checksum report).

| gate | result |
|---|---|
| `go test -race` — `internal/{modkit,content,db/...,web/...}` | 8 packages ok |
| `make check` | **pass** |
| `bin/ggg registry validate` | **pass**, 12 closures verified |
| `make e2e` | **469 passed, 202 skipped, 0 failed, `retried passes: none`** — includes the new dateless case |
| `make visual` | **200 passed** after the 4-baseline update; a confirming re-run is green with exactly those 4 files modified |
| `bin/ggg sync --check --offline` | clean, inside this commit |

Release order run last so the signed snapshot describes both slices:
`registry build` → `registry sign` → `sync --offline` → `sync --check --offline`,
snapshot `ba3a3a91233259f2…`.

Revisions bumped this round, each read fresh from disk immediately before
writing rather than from a quoted value:

| module | revision | why |
|---|---|---|
| `ggg/page/admin-content` | 1 → 2 | the templ badge and the page handler |
| `ggg/system/content` | 3 → 5 | `ListEntriesAdmin`, `content_cms_test.go`, six reclassifications, then the new seed payload |
| `ggg/workflow/admin-content` | 4 → 5 | the new e2e case |
| `ggg/system/api` | 1 → 2 | two reclassifications |
| `ggg/system/content-assets` | 10 → 11 | `content/docs/content.md` |
| `ggg/system/modkit` | 32 → 33 | `test_scan.go`, `test_scan_test.go`, `shell_scan.go`, all changed after 32 was released |
| `ggg/system/e2e-sweeps` | 2 → 3 | owns the four rewritten baselines |

## One line for the parent's own note, because it is the same lesson

The export failure was never a storage defect: `ggg test e2e` brought up the
compose `app` service, which runs a second jobs worker against the same test
database, so whichever worker won the SKIP-LOCKED claim wrote the CSV into its
own container-local object store while Playwright drove the host server. One
database, two stores. The `Close`-swallowing hypothesis in the brief — and the
one I repeated in my own first report — was reasoning from the shape of the
code instead of measuring where the bytes went. That is the same error as
attributing item 4 to load average without measuring it, and the same error as
my own first selector fix, which I reasoned about instead of running against
the real tree. Measure the thing, then deviate.
