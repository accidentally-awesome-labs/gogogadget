package jobs

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/db/testdb"
	"github.com/gogogadget/gogogadget/internal/mail"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testSetup(t *testing.T) (*pgxpool.Pool, *sqlc.Queries) {
	t.Helper()
	pool, q := testdb.Open(t, "jobs")
	_, err := pool.Exec(context.Background(), "DELETE FROM jobs")
	require.NoError(t, err)
	return pool, q
}

func testWorker(q *sqlc.Queries, dir string) *Worker {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewWorker(q, mail.NewDevSender(log, dir), log)
}

func TestEnqueueProcessComplete(t *testing.T) {
	_, q := testSetup(t)
	ctx := context.Background()
	dir := t.TempDir()
	w := testWorker(q, dir)

	require.NoError(t, EnqueueEmail(ctx, q, KindWelcome, mail.Message{
		To: "new@example.com", Subject: "Welcome", HTML: "<h1>hi</h1>", Text: "hi",
	}, "", time.Time{}))

	done, err := w.ProcessOne(ctx)
	require.NoError(t, err)
	assert.True(t, done)

	// Job completed → not reclaimable.
	done, err = w.ProcessOne(ctx)
	require.NoError(t, err)
	assert.False(t, done)

	// DevSender wrote the viewable HTML file.
	matches, err := filepath.Glob(filepath.Join(dir, "*-new_example.com.html"))
	require.NoError(t, err)
	require.Len(t, matches, 1)
	raw, err := os.ReadFile(matches[0])
	require.NoError(t, err)
	assert.Contains(t, string(raw), "<h1>hi</h1>")
}

// A poison job is a KNOWN kind whose handler keeps failing — a malformed payload
// makes the email arm fail on unmarshal every attempt. It deliberately does not
// use an unknown kind any more: an unknown kind is an uninstalled module and now
// dies on the first claim, which is a different contract (see
// TestUnknownKindDeadLettersImmediately).
func TestPoisonJobFailsWithBackoff(t *testing.T) {
	pool, q := testSetup(t)
	ctx := context.Background()
	w := testWorker(q, t.TempDir())

	// org_id is a string in EmailPayload, so a number fails to unmarshal.
	require.NoError(t, Enqueue(ctx, q, KindWelcome, map[string]any{"org_id": 12345}))

	done, err := w.ProcessOne(ctx)
	require.NoError(t, err)
	assert.True(t, done)

	// After one failure: attempts=1, error recorded, run_at pushed ~2^1 min out.
	var attempts int32
	var lastError string
	var runAt time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT attempts, COALESCE(last_error,''), run_at FROM jobs`).Scan(&attempts, &lastError, &runAt))
	assert.Equal(t, int32(1), attempts)
	assert.Contains(t, lastError, "cannot unmarshal")
	assert.True(t, runAt.After(time.Now().Add(time.Minute)), "backoff must push run_at into the future")

	// So an immediate re-claim finds nothing.
	done, err = w.ProcessOne(ctx)
	require.NoError(t, err)
	assert.False(t, done)
}

func TestDeadLetterAtMaxAttempts(t *testing.T) {
	pool, q := testSetup(t)
	ctx := context.Background()
	w := testWorker(q, t.TempDir())

	var deadLettered string
	w.OnDeadLetter = func(kind string, err error) { deadLettered = kind }

	require.NoError(t, Enqueue(ctx, q, KindWelcome, map[string]any{"org_id": 12345}))
	for i := 0; i < 8; i++ {
		done, err := w.ProcessOne(ctx)
		require.NoError(t, err)
		require.True(t, done, "cycle %d: expected the job to be claimable", i)
		// Fast-forward past the backoff so the next cycle can claim it.
		_, err = pool.Exec(ctx, `UPDATE jobs SET run_at = now() WHERE done_at IS NULL`)
		require.NoError(t, err)
	}
	assert.Equal(t, KindWelcome, deadLettered)

	// Dead-lettered: done_at set, last_error='exhausted', never claimable again.
	var lastError string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COALESCE(last_error,'') FROM jobs WHERE done_at IS NOT NULL`).Scan(&lastError))
	assert.Equal(t, "exhausted", lastError)
	done, err := w.ProcessOne(ctx)
	require.NoError(t, err)
	assert.False(t, done)
}

func TestTrialEndingGuardSkipsNonTrialing(t *testing.T) {
	pool, q := testSetup(t)
	ctx := context.Background()

	// Org with an ACTIVE subscription: a stale trial-ending job must not send.
	_, err := pool.Exec(ctx, `INSERT INTO users (clerk_user_id, email) VALUES ('user_g', 'g@example.com') ON CONFLICT DO NOTHING`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO orgs (clerk_org_id, name, slug) VALUES ('org_g', 'G', 'g') ON CONFLICT DO NOTHING`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO subscriptions (clerk_org_id, polar_customer_id, product_key, status)
		VALUES ('org_g', 'cust_g', 'pro', 'active') ON CONFLICT (clerk_org_id) DO UPDATE SET status = 'active'`)
	require.NoError(t, err)
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM subscriptions WHERE clerk_org_id = 'org_g'`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM orgs WHERE clerk_org_id = 'org_g'`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE clerk_user_id = 'user_g'`)
	}()

	dir := t.TempDir()
	w := testWorker(q, dir)
	require.NoError(t, EnqueueAt(ctx, q, KindTrialEnding, EmailPayload{
		To: "g@example.com", Subject: "trial", HTML: "x", Text: "x", OrgID: "org_g",
	}, time.Time{}))

	done, err := w.ProcessOne(ctx)
	require.NoError(t, err)
	assert.True(t, done)

	// Completed WITHOUT sending (no email file written).
	matches, err := filepath.Glob(filepath.Join(dir, "*.html"))
	require.NoError(t, err)
	assert.Empty(t, matches)
}

func TestSchedulerClaimsDueAndEnqueues(t *testing.T) {
	pool, q := testSetup(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, "DELETE FROM schedules")
	require.NoError(t, err)
	w := testWorker(q, t.TempDir())

	// Due now.
	due, err := q.CreateSchedule(ctx, sqlc.CreateScheduleParams{
		Name: "digest-due", Kind: KindEmailDigest, Payload: []byte(`{}`),
		EverySeconds: 300, NextRunAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Second), Valid: true},
	})
	require.NoError(t, err)
	// Not due yet.
	_, err = q.CreateSchedule(ctx, sqlc.CreateScheduleParams{
		Name: "digest-later", Kind: KindEmailDigest, Payload: []byte(`{}`),
		EverySeconds: 300, NextRunAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)
	// Disabled schedule due now — must NOT be claimed.
	_, err = q.CreateSchedule(ctx, sqlc.CreateScheduleParams{
		Name: "digest-off", Kind: KindEmailDigest, Payload: []byte(`{}`),
		EverySeconds: 300, NextRunAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Second), Valid: true},
	})
	require.NoError(t, err)
	off, err := pool.Exec(ctx, "UPDATE schedules SET enabled = FALSE WHERE name = 'digest-off'")
	require.NoError(t, err)
	_ = off

	require.NoError(t, w.schedulerPass(ctx))

	// The due schedule enqueued exactly one wrapped job.
	rows, err := pool.Query(ctx, "SELECT payload FROM jobs WHERE kind = $1", KindEmailDigest)
	require.NoError(t, err)
	var payloads []json.RawMessage
	for rows.Next() {
		var p json.RawMessage
		require.NoError(t, rows.Scan(&p))
		payloads = append(payloads, p)
	}
	rows.Close()
	require.Len(t, payloads, 1)
	var sp SchedulePayload
	require.NoError(t, json.Unmarshal(payloads[0], &sp))
	assert.Equal(t, due.ID, sp.ScheduleID)
	assert.JSONEq(t, `{}`, string(sp.Payload))

	// next_run_at advanced by the interval (missed ticks skipped by design).
	var next time.Time
	require.NoError(t, pool.QueryRow(ctx, "SELECT next_run_at FROM schedules WHERE id = $1", due.ID).Scan(&next))
	assert.True(t, next.After(time.Now().Add(299*time.Second)), "advanced to now + interval")
	assert.True(t, next.Before(time.Now().Add(301*time.Second)))

	// The not-due and disabled rows were untouched.
	var lastNull bool
	require.NoError(t, pool.QueryRow(ctx, "SELECT last_run_at IS NULL FROM schedules WHERE name = 'digest-later'").Scan(&lastNull))
	assert.True(t, lastNull)
	require.NoError(t, pool.QueryRow(ctx, "SELECT last_run_at IS NULL FROM schedules WHERE name = 'digest-off'").Scan(&lastNull))
	assert.True(t, lastNull, "disabled schedule never claims")

	// Second pass with nothing due enqueues nothing new.
	require.NoError(t, w.schedulerPass(ctx))
	var n int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE kind = $1", KindEmailDigest).Scan(&n))
	assert.Equal(t, 1, n)
}

func TestJanitorAppliesAuditRetention(t *testing.T) {
	pool, q := testdb.Open(t, "jobsret")
	ctx := context.Background()

	// Two audit rows: one stale, one fresh — created_at is defaulted to
	// now(), so backdate the stale one directly.
	for _, action := range []string{"retention.stale", "retention.fresh"} {
		if _, err := q.InsertAuditLog(ctx, sqlc.InsertAuditLogParams{Action: action, Metadata: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE audit_log SET created_at = now() - interval '90 days' WHERE action = 'retention.stale'`); err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := NewWorker(q, nil, log)
	w.AuditRetentionDays = 30
	w.janitorPass(ctx)

	rows, err := q.ListAuditAll(ctx, sqlc.ListAuditAllParams{Filter: "retention.", Off: 0, Lim: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Action != "retention.fresh" {
		t.Fatalf("expected only the fresh row, got %d rows", len(rows))
	}

	// Retention disabled (0) must never delete.
	if _, err := pool.Exec(ctx, `UPDATE audit_log SET created_at = now() - interval '400 days' WHERE action = 'retention.fresh'`); err != nil {
		t.Fatal(err)
	}
	w.AuditRetentionDays = 0
	w.janitorPass(ctx)
	rows, err = q.ListAuditAll(ctx, sqlc.ListAuditAllParams{Filter: "retention.", Off: 0, Lim: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("retention=0 deleted rows: %d remain", len(rows))
	}
}

func TestJanitorSweepsExpiredIdempotencyKeys(t *testing.T) {
	pool, q := testdb.Open(t, "jobsidem")
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO orgs (clerk_org_id, name, slug) VALUES ('org_jan','Jan','jan') ON CONFLICT DO NOTHING`)
	require.NoError(t, err)

	// One key inside the retention window, one past it — created_at defaults
	// to now(), so the stale one is backdated directly.
	for _, k := range []string{"fresh", "stale"} {
		_, err = q.ClaimIdempotencyKey(ctx, sqlc.ClaimIdempotencyKeyParams{
			ClerkOrgID: "org_jan", Key: k, Endpoint: "POST /api/v1/projects", RequestHash: "h",
		})
		require.NoError(t, err)
	}
	_, err = pool.Exec(ctx, `UPDATE idempotency_keys SET created_at = now() - $1::interval WHERE key = 'stale'`,
		(IdempotencyRetention + time.Hour).String())
	require.NoError(t, err)

	NewWorker(q, nil, slog.New(slog.NewTextHandler(io.Discard, nil))).janitorPass(ctx)

	_, err = q.GetIdempotencyKey(ctx, sqlc.GetIdempotencyKeyParams{ClerkOrgID: "org_jan", Key: "stale"})
	assert.ErrorIs(t, err, pgx.ErrNoRows, "keys past the retention window are swept")
	_, err = q.GetIdempotencyKey(ctx, sqlc.GetIdempotencyKeyParams{ClerkOrgID: "org_jan", Key: "fresh"})
	assert.NoError(t, err, "a key a client might still retry against must survive")
}

// seedDunningSub creates an org with a subscription in the given status.
func seedDunningSub(t *testing.T, pool poolExec, orgID, status string) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO orgs (clerk_org_id, name, slug) VALUES ($1,$1,$1) ON CONFLICT DO NOTHING`, orgID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO users (clerk_user_id, email, name) VALUES ($1, $1 || '@example.com', 'U') ON CONFLICT DO NOTHING`, "u_"+orgID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO org_members (clerk_org_id, clerk_user_id, role) VALUES ($1, $2, 'org:admin') ON CONFLICT DO NOTHING`, orgID, "u_"+orgID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO subscriptions (clerk_org_id, polar_customer_id, product_key, status)
		VALUES ($1, 'cus', 'pro', $2)
		ON CONFLICT (clerk_org_id) DO UPDATE SET status = EXCLUDED.status`, orgID, status)
	require.NoError(t, err)
}

// The dunning follow-ups are scheduled days ahead, so by send time the card
// may already be fixed. Few things burn goodwill faster than "your payment is
// failing" arriving after it succeeded.
func TestDunningEmailSkippedAfterRecovery(t *testing.T) {
	pool, q := testdb.Open(t, "jobsdunning")
	ctx := context.Background()
	_, err := pool.Exec(ctx, "DELETE FROM jobs")
	require.NoError(t, err)
	seedDunningSub(t, pool, "org_recovered", "active")

	sender := &captureSender{}
	w := NewWorker(q, sender, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, EnqueueEmail(ctx, q, KindDunningFinal, mail.Message{
		To: "a@example.com", Subject: "Final notice", HTML: "<p>x</p>", Text: "x",
	}, "org_recovered", time.Time{}))

	done, err := w.ProcessOne(ctx)
	require.NoError(t, err)
	assert.True(t, done, "the job runs…")
	assert.Empty(t, sender.sent, "…and sends nothing, because the payment recovered")
}

func TestDunningEmailSentWhileStillPastDue(t *testing.T) {
	pool, q := testdb.Open(t, "jobsdunning")
	ctx := context.Background()
	_, err := pool.Exec(ctx, "DELETE FROM jobs")
	require.NoError(t, err)
	seedDunningSub(t, pool, "org_stillbad", "past_due")

	sender := &captureSender{}
	w := NewWorker(q, sender, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, EnqueueEmail(ctx, q, KindDunningReminder, mail.Message{
		To: "b@example.com", Subject: "Still failing", HTML: "<p>x</p>", Text: "x",
	}, "org_stillbad", time.Time{}))

	done, err := w.ProcessOne(ctx)
	require.NoError(t, err)
	require.True(t, done)
	require.Len(t, sender.sent, 1)
	assert.Equal(t, "b@example.com", sender.sent[0].To)
}

// The final notice also re-notifies in-app: the day-0 notification is a week
// stale by the time this runs.
func TestFinalDunningNotifiesInApp(t *testing.T) {
	pool, q := testdb.Open(t, "jobsdunning")
	ctx := context.Background()
	_, err := pool.Exec(ctx, "DELETE FROM jobs")
	require.NoError(t, err)
	seedDunningSub(t, pool, "org_final", "past_due")

	w := NewWorker(q, &captureSender{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, EnqueueEmail(ctx, q, KindDunningFinal, mail.Message{
		To: "c@example.com", Subject: "Final", HTML: "<p>x</p>", Text: "x",
	}, "org_final", time.Time{}))
	_, err = w.ProcessOne(ctx)
	require.NoError(t, err)

	notes, err := q.ListNotificationsByUser(ctx, sqlc.ListNotificationsByUserParams{
		ClerkOrgID: "org_final", ClerkUserID: "u_org_final", Limit: 10, Offset: 0,
	})
	require.NoError(t, err)
	require.NotEmpty(t, notes)
	assert.Contains(t, notes[0].Title, "Final notice")
	assert.Equal(t, "/app/settings/billing", notes[0].Url)
}

// A subscription that vanished (org deleted, never subscribed) must not error
// the job — there is simply nobody to dun.
func TestDunningEmailSkippedWithoutSubscription(t *testing.T) {
	pool, q := testdb.Open(t, "jobsdunning")
	ctx := context.Background()
	_, err := pool.Exec(ctx, "DELETE FROM jobs")
	require.NoError(t, err)

	sender := &captureSender{}
	w := NewWorker(q, sender, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, EnqueueEmail(ctx, q, KindDunningReminder, mail.Message{
		To: "d@example.com", Subject: "x", HTML: "<p>x</p>", Text: "x",
	}, "org_gone", time.Time{}))

	done, err := w.ProcessOne(ctx)
	require.NoError(t, err, "a missing subscription is a skip, not a failure")
	assert.True(t, done)
	assert.Empty(t, sender.sent)
}

// A ticker alone put the first janitor sweep 24 hours after process start and
// reset that clock on every restart, so any deployment recycling more often than
// daily never reached it. Retention that only holds on a long-uptime host is not
// retention, and every consequence was silent: unbounded job rows, an unbounded
// inbound-idempotency table, AUDIT_RETENTION_DAYS never enforced, rotated
// webhook secrets never cleared.
func TestJanitorRunsBeforeItsFirstTick(t *testing.T) {
	pool, q := testdb.Open(t, "jobs")
	defer pool.Close()

	stale := time.Now().Add(-30 * 24 * time.Hour)
	_, err := pool.Exec(t.Context(),
		`INSERT INTO jobs (kind, payload, attempts, max_attempts, run_at, done_at, created_at)
		 VALUES ('email.digest', '{}', 1, 8, $1, $1, $1)`, stale)
	require.NoError(t, err)

	before := countJobs(t, pool)
	require.Positive(t, before, "the fixture row must exist, or this proves nothing")

	// Run with a live context and stop as soon as the sweep is observable. A
	// cancelled context would make the sweep a no-op, which would pass for the
	// wrong reason: the query would fail rather than the row being kept.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	log := slog.New(slog.DiscardHandler)
	go NewWorker(q, mail.NewDevSender(log, t.TempDir()), log).Run(ctx)

	deadline := time.Now().Add(10 * time.Second)
	for countJobs(t, pool) >= before && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	assert.Less(t, countJobs(t, pool), before,
		"the janitor must sweep on start; waiting for the first 24h tick means it never runs on a host that restarts daily")
}

func countJobs(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM jobs`).Scan(&n))
	return n
}

// Run is started in a goroutine by Module.Start with no recover above it, so
// before this a panicking handler unwound past the web server and took the whole
// process down. A background worker must not be able to kill the thing serving
// traffic, and the queue already has the right answer for a handler that cannot
// complete: fail it, back off, dead-letter at the budget.
func TestAPanickingHandlerFailsItsJobRatherThanTheProcess(t *testing.T) {
	pool, q := testdb.Open(t, "jobs")
	defer pool.Close()

	log := slog.New(slog.DiscardHandler)
	w := NewWorker(q, mail.NewDevSender(log, t.TempDir()), log)
	w.definitions["test.panic"] = Define("test.panic", false, 2,
		func(context.Context, struct{}) error { panic("handler exploded") })

	require.NoError(t, Enqueue(t.Context(), q, "test.panic", struct{}{}))

	handled, err := w.ProcessOne(t.Context())
	require.True(t, handled, "the job must be claimed and handled")
	require.NoError(t, err, "a panicking handler is a failed job, not a failed worker")

	var attempts int
	var lastError pgtype.Text
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT attempts, last_error FROM jobs WHERE kind = 'test.panic'`).Scan(&attempts, &lastError))
	assert.Equal(t, 1, attempts, "the attempt must be recorded so the backoff schedule advances")
	assert.Contains(t, lastError.String, "panicked",
		"the recorded error must name the panic, or the operator sees a failure with no cause")
}

// The guard above covers a panicking HANDLER. The claim itself sat outside it:
// a worker whose queue cannot execute at all panics inside ClaimJob, one frame
// below any recover, and unwinds out of the goroutine Module.Start launched -
// taking the web server with it. CI caught exactly that, from a Worker built on
// a zero-value Queries whose pool is nil.
//
// A worker that cannot reach its queue must back off and keep the process
// alive, which is what reporting zero work does: the caller sleeps a poll
// interval instead of spinning on the failure.
func TestAPanickingClaimKeepsTheWorkerAliveRatherThanTheProcess(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	// A Queries with no pool behind it: every statement it runs dereferences
	// nil. Constructible, and therefore reachable - the constructor can only
	// check that the dependency is present, not that it can talk to Postgres.
	w := NewWorker(&sqlc.Queries{}, mail.NewDevSender(log, t.TempDir()), log)

	assert.NotPanics(t, func() {
		assert.Zero(t, w.pass(t.Context()),
			"a pass that could not claim anything must report no work, so the loop backs off")
	})
}

// Run must survive the same failure end to end: the loop keeps going and Stop
// still ends it. This is the shape the module lifecycle test hits, and before
// the guard it was a race between the first claim and the cancel.
func TestRunSurvivesAnUnusableQueueUntilTheContextEnds(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	w := NewWorker(&sqlc.Queries{}, mail.NewDevSender(log, t.TempDir()), log)
	w.poll = time.Millisecond

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()

	// Long enough for several passes, each of which panics and recovers.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}
