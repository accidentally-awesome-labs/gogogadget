package jobs

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/db/testdb"
	"github.com/gogogadget/gogogadget/internal/mail"

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

func TestPoisonJobFailsWithBackoff(t *testing.T) {
	pool, q := testSetup(t)
	ctx := context.Background()
	w := testWorker(q, t.TempDir())

	require.NoError(t, Enqueue(ctx, q, "unknown.kind", map[string]string{"x": "y"}))

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
	assert.Contains(t, lastError, "unknown job kind")
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

	require.NoError(t, Enqueue(ctx, q, "unknown.kind", nil))
	for i := 0; i < 8; i++ {
		done, err := w.ProcessOne(ctx)
		require.NoError(t, err)
		require.True(t, done, "cycle %d: expected the job to be claimable", i)
		// Fast-forward past the backoff so the next cycle can claim it.
		_, err = pool.Exec(ctx, `UPDATE jobs SET run_at = now() WHERE done_at IS NULL`)
		require.NoError(t, err)
	}
	assert.Equal(t, "unknown.kind", deadLettered)

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
