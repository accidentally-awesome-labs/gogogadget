---
title: Background jobs
description: The Postgres job queue — SKIP LOCKED claims, backoff, dead-letters.
section: Features
weight: 11
---

Anything that shouldn't block a request — transactional and digest email, usage
flushes, outbound webhook delivery, account and organization exports — goes
through a **Postgres-backed job queue** in `internal/jobs`. No Redis, no extra
infra: the same database that serves the app backs the queue, and the claim
query is safe to run from multiple worker processes.

## The claim query

`jobs.Module.Start` launches one worker goroutine during `modules.Boot`, and it
polls every **2 seconds, jittered** by up to one extra second. Each poll drains
every currently-claimable job using this query:

```sql
UPDATE jobs
SET attempts = attempts + 1, last_error = NULL, run_at = now() + interval '5 minutes'
WHERE id = (
  SELECT id FROM jobs
  WHERE done_at IS NULL AND run_at <= now()
  ORDER BY run_at
  LIMIT 1
  FOR UPDATE SKIP LOCKED
)
RETURNING *;
```

Two properties are load-bearing:

- **`FOR UPDATE SKIP LOCKED`** — concurrent workers skip rows others hold, so
  the same table can be polled by many processes (or machines) without two
  workers claiming the same row in the same instant.
- **The 5-minute visibility timeout** — claiming pushes `run_at` five minutes
  into the future instead of deleting anything. If the worker crashes mid-job,
  the job **reappears** when the timeout lapses rather than being lost.

`pgx.ErrNoRows` from the claim means the queue is empty: the worker sleeps
and polls again.

### Delivery is at-least-once, and handlers must be idempotent

The visibility timeout is a crash-recovery window, **not a lease**. Nothing
renews it while a handler runs, and `CompleteJob` is `UPDATE jobs SET done_at
= now() WHERE id = $1` — no ownership or attempt fence. So a handler still
running five minutes after its claim can be claimed again by another worker
(or by the same worker's next poll) and run **concurrently with itself**;
whichever run finishes first marks the row done, and the other's completion is
a no-op.

That is the real contract: **at least once**, never exactly once. Write every
handler so a second concurrent execution is harmless — key external calls by
something stable (see the `webhook-id` derived from the delivery row in
`internal/jobs/webhooks_deliver.go`), re-check the world before acting (see
run-time guards below), and treat "already done" as success rather than as an
error. A job that takes minutes should either stay well inside the window or
carry its own idempotency key.

## Failure, backoff, dead-letter

On dispatch error the worker records `last_error` and reschedules with
**exponential backoff — `2^attempts` minutes** (2, 4, 8, …):

```sql
UPDATE jobs SET last_error = $2, run_at = now() + (interval '1 minute' * power(2, attempts))
WHERE id = $1;
```

A panicking handler is not a process crash: the worker recovers it, logs the
stack, and treats it as a failed attempt, because a background handler must not
be able to take down the goroutine serving traffic.

A job is dead-lettered — `done_at` set, `last_error` set to a terminal reason,
logged, and reported through the worker's `OnDeadLetter` hook (wired to Sentry
when `SENTRY_DSN` is configured, see [Observability](/docs/observability)) —
for one of exactly two reasons:

- **`exhausted`** — `attempts` reached the row's `max_attempts` (default
  **8**).
- **`module_uninstalled`** — the row's `kind` is not in the generated
  dispatcher, so no installed module provides a handler for it. This is
  terminal on the **first** claim, with `attempts` at 1: retrying a handler
  that cannot exist would burn the whole backoff schedule — hours of queue
  capacity — and bury the real signal under repeated failures. It is what you
  see after removing a module that still had queued work, and the fix is to
  reinstall the module or accept the loss, not to requeue.

Dead-lettered rows stay in the table for inspection until the janitor
removes them.

## The janitor

The same goroutine runs the cleanup sweeps: **once when the worker starts**, and
then on a 24-hour ticker. The pass on start is what makes retention hold on a
host that restarts more often than once a day — a ticker-only pass meant a
frequently deployed app enforced nothing.

The sweeps are generated from the module manifests that own the tables, so a
module cannot add a table and forget its retention. The selected set today:

- **`old_jobs`** — `jobs` with `done_at` older than **7 days** are deleted.
- **`webhook_events`** — delivered events older than **30 days** are deleted
  (idempotency records only need to outlive provider retry windows — see
  [Billing](/docs/billing) and [Organizations](/docs/organizations)).
- **`webhook_secrets`** — rotated-out signing secrets are cleared once the
  rotation grace window passes, so a leaked old secret stops being accepted.
- **`idempotency_keys`** — stored API responses stop being replayable after
  **24 hours**: the table is a cache, not an archive.
- **`audit_log`** — trimmed to `AUDIT_RETENTION_DAYS`. Zero means retain
  forever, which is the compliance-safe default.

Each sweep is logged independently and one failure does not stop the others; a
panicking sweep is recovered, because losing retention is a much smaller failure
than losing the claim loop.

## Enqueueing

```go
jobs.Enqueue(ctx, q, kind, payload)              // run now
jobs.EnqueueAt(ctx, q, kind, payload, runAt)     // run at a time
jobs.EnqueueEmail(ctx, q, kind, msg, orgID, at)  // email contract
```

Payloads are marshaled to JSONB and must be **self-contained**: rendered
email bodies, IDs, timestamps — never template references or request-scoped
values. The email payload contract (`to`, `subject`, `html`, `text`,
`org_id?`) and why bodies are rendered at enqueue time are covered in
[Email](/docs/email).

## Run-time guards

A job executes long after it was enqueued, so dispatch may re-check the world
before acting. The shipped example: `email.trial_ending` re-reads the
subscription and **skips the send unless the status is still `trialing`** —
a customer who converted in the meantime never gets "your trial ends soon".
Skips are logged and complete the job normally; they are not failures and
don't consume retries.

## Recurring schedules

One-off jobs cover most work; **recurring** work lives in the `schedules`
table and the same worker's scheduler pass, which runs each poll cycle.
There is no cron daemon: `ClaimDueSchedules` atomically flips a due row to
`last_run_at = now(), next_run_at = now() + interval` (with `FOR UPDATE SKIP
LOCKED`, so many workers may share it) and returns it; the pass then enqueues
the schedule's `kind` with the wrapped payload:

```go
jobs.SchedulePayload{ScheduleID: s.ID, Payload: s.Payload}
```

A schedulable handler takes `SchedulePayload` as its payload type, so the
wrapper is unmarshalled for it and `.Payload` is the schedule's own data. Only
a kind whose declaration says `schedulable` may back a schedule row —
`SchedulableKinds` is generated from those declarations, because a schedule
pointing at a one-shot handler would fire it forever.

**Missed ticks are skipped by design** — a schedule that was down for an hour
fires once and advances from *now*, never catch-up storms. `every_seconds >= 60`
is a table CHECK.

Seeded for the dev database: `usage-flush` (`usage.flush`, every 300s — feeds
the metering job) and `email-digest` (`email.digest`, every 3600s). The digest
pass looks hourly and mails only the users whose own cadence is due — see
[Email](/docs/email).

Create schedules from seeds, admin tooling, or your own code with the
`schedules` helper:

```go
schedules.Create(ctx, q, schedules.Schedule{
    Name: "nightly-rollup", Kind: "metrics.rollup",
    Payload: map[string]any{"granularity": "day"},
    EverySeconds: 86400, // ClerkOrgID empty = system-wide
})
```

## Adding a job kind

There is no dispatch switch to edit. `Worker.dispatch` looks the kind up in a
**generated** table, and that table is built from module manifests — so a kind
exists for the queue exactly when a module declares it.

1. Add a `Kind<Name> = "<domain>.<action>"` constant, and a payload struct with
   JSON tags (or reuse one). The payload must be self-contained.
2. Write the handler as `func(context.Context, P) error` and wrap it in a
   declaration constructor beside the other ones in
   `internal/jobs/definitions.go` (or in your module's own file):

   ```go
   func (w *Worker) defineMetricsRollup() Definition {
       return Define(KindMetricsRollup, true, 0, w.rollupMetrics)
   }
   ```

   `Define` is generic over the payload, so dispatch stays data while the
   handler stays typed. The three arguments after the kind are the contract:
   `schedulable` decides whether a `schedules` row may reference the kind
   (`SchedulableKinds` is derived from it, because a schedule pointing at a
   one-shot handler would fire it forever), and `maxAttempts` is the retry
   budget — `0` means "unspecified" and normalizes to
   `jobs.DefaultMaxAttempts` (**8**), never "never retry". Use
   `DefineWithAttempt` when the handler needs to know it is on its last
   attempt, as webhook delivery does to mark the delivery row dead exactly when
   the queue gives up.
3. Declare it in the owning module's manifest under `runtime.jobs`:

   ```json
   { "kind": "metrics.rollup", "package": "internal/jobs",
     "handler": "defineMetricsRollup", "schedulable": true, "max_attempts": 0 }
   ```
4. Run `make generate` (or `go run ./cmd/ggg sync --offline`). That regenerates
   `internal/jobs/jobs_registry_gen.go`: the `workerDefinitions` table, the
   `SchedulableKinds` list, and `declaredAttempts` — the map the enqueue helpers
   write onto each new row. A manifest naming a constructor that does not exist
   is a compile error on a named generated line; a constructor with no manifest
   entry is never dispatched, and its persisted rows dead-letter as
   `module_uninstalled`.
5. Enqueue from the event source with `jobs.Enqueue` / `jobs.EnqueueAt`. The
   declared budget is written into the row's `max_attempts` at enqueue time, and
   the **row** is dispatch truth from then on — changing a declaration does not
   re-budget work that is already queued.

Test it the way the shipped kinds are tested: enqueue, run `ProcessOne` once
with a `DevSender`, and assert `done_at` is set; a poison payload should
increment `attempts` and advance `run_at`. See [Testing](/docs/testing).
