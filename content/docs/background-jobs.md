---
title: Background jobs
description: The Postgres job queue — SKIP LOCKED claims, backoff, dead-letters.
section: Features
weight: 10
---

Anything that shouldn't block a request — every email today — goes through a
**Postgres-backed job queue** in `internal/jobs/jobs.go`. No Redis, no extra
infra: the same database that serves the app backs the queue, and the claim
query is safe to run from multiple worker processes.

## The claim query

One worker goroutine starts in `cmd/server/main.go` and polls every **2
seconds, jittered** by up to one extra second. Each poll drains every
currently-claimable job using this query:

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
  the same table can be polled by many processes (or machines) without
  double-claiming.
- **The 5-minute visibility timeout** — claiming pushes `run_at` five minutes
  into the future instead of deleting anything. If the worker crashes
  mid-job, the job **reappears** when the timeout lapses rather than being
  lost or claimed by a second worker while still in flight.

`pgx.ErrNoRows` from the claim means the queue is empty: the worker sleeps
and polls again.

## Failure, backoff, dead-letter

On dispatch error the worker records `last_error` and reschedules with
**exponential backoff — `2^attempts` minutes** (2, 4, 8, …):

```sql
UPDATE jobs SET last_error = $2, run_at = now() + (interval '1 minute' * power(2, attempts))
WHERE id = $1;
```

When `attempts` reaches `max_attempts` (default **8**), the job is
dead-lettered: `done_at` set, `last_error = 'exhausted'`, logged, and
reported through the worker's `OnDeadLetter` hook — wired to Sentry when
`SENTRY_DSN` is configured (see [Observability](/docs/observability)).
Dead-lettered rows stay in the table for inspection until the janitor
removes them.

## The janitor

The same goroutine runs a **daily** cleanup pass on a 24-hour ticker:

- `jobs` with `done_at` older than **7 days** are deleted.
- `webhook_events` older than **30 days** are deleted (idempotency records
  only need to outlive provider retry windows — see
  [Billing](/docs/billing) and [Organizations](/docs/organizations)).

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

Dispatch cases that accept scheduled work unwrap `.Payload`. **Missed ticks
are skipped by design** — a schedule that was down for an hour fires once and
advances from *now*, never catch-up storms. `every_seconds >= 60` is a table
CHECK.

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

1. Add a `Kind<Name> = "<domain>.<action>"` constant in
   `internal/jobs/jobs.go`.
2. Define a payload struct with JSON tags (or reuse an existing one).
3. Add a `case` to `Worker.dispatch` that unmarshals the payload and does the
   work — returning an error triggers the backoff/dead-letter machinery for
   free.
4. Enqueue from the event source with `jobs.Enqueue` / `jobs.EnqueueAt`.

Test it the way the shipped kinds are tested: enqueue, run `ProcessOne` once
with a `DevSender`, and assert `done_at` is set; a poison payload should
increment `attempts` and advance `run_at`. See [Testing](/docs/testing).
