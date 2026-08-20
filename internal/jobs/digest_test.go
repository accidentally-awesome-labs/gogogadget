package jobs

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/db/testdb"
	"github.com/gogogadget/gogogadget/internal/mail"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureSender records what would have been delivered. The digest is the one
// email the worker renders itself, so the assertions are about content and
// recipients, not just "a job ran".
type captureSender struct {
	mu   sync.Mutex
	sent []mail.Message
	err  error
}

func (c *captureSender) Send(_ context.Context, m mail.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	c.sent = append(c.sent, m)
	return nil
}

func (c *captureSender) to() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.sent))
	for _, m := range c.sent {
		out = append(out, m.To)
	}
	return out
}

func digestWorker(t *testing.T, q *sqlc.Queries, sender mail.Sender) *Worker {
	t.Helper()
	w := NewWorker(q, sender, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.AppURL = "https://app.example.test"
	return w
}

func seedDigestUser(t *testing.T, pool poolExec, id, email, freq string, lastDigest any) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO users (clerk_user_id, email, name, digest_frequency, last_digest_at)
		VALUES ($1, $2, 'Digest User', $3, $4)
		ON CONFLICT (clerk_user_id) DO UPDATE SET digest_frequency = EXCLUDED.digest_frequency,
			last_digest_at = EXCLUDED.last_digest_at`, id, email, freq, lastDigest)
	require.NoError(t, err)
}

func seedDigestOrgAndNotification(t *testing.T, pool poolExec, orgID, userID, title string) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO orgs (clerk_org_id, name, slug) VALUES ($1, $1, $1) ON CONFLICT DO NOTHING`, orgID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO notifications (clerk_org_id, clerk_user_id, kind, title, body, url)
		VALUES ($1, $2, 'export.ready', $3, 'Body text', '/app/files')`, orgID, userID, title)
	require.NoError(t, err)
}

func TestDigestSendsOnlyToDueUsersWithContent(t *testing.T) {
	pool, q := testdb.Open(t, "jobsdigest")
	ctx := context.Background()
	clearDigestFixtures(t, pool)

	cap := &captureSender{}
	w := digestWorker(t, q, cap)

	// never digested + has a notification → sends
	seedDigestUser(t, pool, "u_due", "due@example.com", "weekly", nil)
	seedDigestOrgAndNotification(t, pool, "org_dig", "u_due", "Export ready")
	// never digested, nothing happened → stamped, no email
	seedDigestUser(t, pool, "u_quiet", "quiet@example.com", "weekly", nil)
	// opted out, with content → never mailed
	seedDigestUser(t, pool, "u_off", "off@example.com", "off", nil)
	seedDigestOrgAndNotification(t, pool, "org_dig", "u_off", "Ignored")
	// digested an hour ago on a weekly cadence → not due yet
	seedDigestUser(t, pool, "u_recent", "recent@example.com", "weekly", time.Now().Add(-time.Hour))
	seedDigestOrgAndNotification(t, pool, "org_dig", "u_recent", "Too soon")

	require.NoError(t, w.sendDigests(ctx, sqlc.Job{Kind: KindEmailDigest}))

	assert.Equal(t, []string{"due@example.com"}, cap.to(),
		"only an opted-in, due user with something to report gets mail")

	quiet := getUser(t, q, "u_quiet")
	assert.True(t, quiet.LastDigestAt.Valid,
		"a quiet account is stamped anyway — otherwise every pass rescans it forever")
	off := getUser(t, q, "u_off")
	assert.False(t, off.LastDigestAt.Valid, "an opted-out user is never touched")
}

func TestDigestStampsAfterSendAndStopsRepeating(t *testing.T) {
	pool, q := testdb.Open(t, "jobsdigest")
	ctx := context.Background()
	clearDigestFixtures(t, pool)

	cap := &captureSender{}
	w := digestWorker(t, q, cap)
	seedDigestUser(t, pool, "u_once", "once@example.com", "daily", nil)
	seedDigestOrgAndNotification(t, pool, "org_dig", "u_once", "First thing")

	require.NoError(t, w.sendDigests(ctx, sqlc.Job{}))
	require.Len(t, cap.sent, 1)

	// Same pass again: the stamp makes the user no longer due.
	require.NoError(t, w.sendDigests(ctx, sqlc.Job{}))
	assert.Len(t, cap.sent, 1, "a second pass must not re-send the same digest")
}

// Delivery failure must leave the user due: the stamp is also the next
// window's start, so stamping on failure would drop that period's content.
func TestDigestLeavesUserDueWhenSendFails(t *testing.T) {
	pool, q := testdb.Open(t, "jobsdigest")
	ctx := context.Background()
	clearDigestFixtures(t, pool)

	failing := &captureSender{err: assert.AnError}
	w := digestWorker(t, q, failing)
	seedDigestUser(t, pool, "u_fail", "fail@example.com", "daily", nil)
	seedDigestOrgAndNotification(t, pool, "org_dig", "u_fail", "Unsent")

	require.Error(t, w.sendDigests(ctx, sqlc.Job{}), "the job must fail so the queue retries it")
	u := getUser(t, q, "u_fail")
	assert.False(t, u.LastDigestAt.Valid, "an unsent digest must stay owed")
}

// The window starts at the previous digest, so items from before it are not
// repeated and items after it are not lost.
func TestDigestWindowStartsAtLastSend(t *testing.T) {
	pool, q := testdb.Open(t, "jobsdigest")
	ctx := context.Background()
	clearDigestFixtures(t, pool)

	cap := &captureSender{}
	w := digestWorker(t, q, cap)
	seedDigestUser(t, pool, "u_win", "win@example.com", "daily", time.Now().Add(-48*time.Hour))
	seedDigestOrgAndNotification(t, pool, "org_dig", "u_win", "Old news")
	_, err := pool.Exec(ctx, `UPDATE notifications SET created_at = now() - interval '72 hours' WHERE title = 'Old news'`)
	require.NoError(t, err)
	seedDigestOrgAndNotification(t, pool, "org_dig", "u_win", "Fresh news")

	require.NoError(t, w.sendDigests(ctx, sqlc.Job{}))
	require.Len(t, cap.sent, 1)
	body := cap.sent[0].Text + cap.sent[0].HTML
	assert.Contains(t, body, "Fresh news")
	assert.NotContains(t, body, "Old news", "content from before the last digest was already reported")
}

func TestDigestEmailLinksAndContent(t *testing.T) {
	pool, q := testdb.Open(t, "jobsdigest")
	ctx := context.Background()
	clearDigestFixtures(t, pool)

	cap := &captureSender{}
	w := digestWorker(t, q, cap)
	seedDigestUser(t, pool, "u_link", "link@example.com", "daily", nil)
	seedDigestOrgAndNotification(t, pool, "org_dig", "u_link", "Export ready")

	require.NoError(t, w.sendDigests(ctx, sqlc.Job{}))
	require.Len(t, cap.sent, 1)
	m := cap.sent[0]
	assert.NotEmpty(t, m.Subject)
	assert.Contains(t, m.HTML, "https://app.example.test/app/files", "notification links are absolute in email")
	assert.Contains(t, m.HTML, "/app/settings/notifications", "every digest carries the way to turn it off")
	assert.Contains(t, m.Text, "Export ready", "a text alternative is always present")
}

func getUser(t *testing.T, q *sqlc.Queries, id string) sqlc.User {
	t.Helper()
	u, err := q.GetUserByClerkID(context.Background(), id)
	require.NoError(t, err)
	return u
}

type poolExec interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func clearDigestFixtures(t *testing.T, pool poolExec) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		"DELETE FROM notifications WHERE clerk_org_id = 'org_dig'",
		"DELETE FROM users WHERE clerk_user_id LIKE 'u\\_%'",
		"DELETE FROM orgs WHERE clerk_org_id = 'org_dig'",
	} {
		_, err := pool.Exec(ctx, stmt)
		require.NoError(t, err)
	}
}
