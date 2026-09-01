package jobs

import (
	"context"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/mail"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	// digestBatch caps one run. A schedule pass that would email the entire
	// user table in a single job is a job that times out and retries from
	// zero; the stamp makes the next run resume where this one stopped.
	digestBatch = 200
	// digestMaxItems keeps a rollup readable — and keeps one very active
	// account from rendering a megabyte of HTML into a mail client.
	digestMaxItems = 25
	// digestFirstWindow is how far back the first-ever digest reaches, since
	// there is no previous stamp to bound it.
	digestFirstWindow = 7 * 24 * time.Hour
)

// sendDigests emails each due user a rollup of the notifications they
// received during their window. Cadence is per user (users.digest_frequency);
// the schedule only decides how often we *look*.
//
// Ordering is deliberate: send, then stamp. The stamp doubles as the next
// window's start, so writing it first would drop that period's content on a
// delivery failure. The cost is that a crash between send and stamp can
// repeat one digest — a duplicate summary is a far smaller harm than a
// silently skipped one.
func (w *Worker) sendDigests(ctx context.Context, _ SchedulePayload) error {
	due, err := w.q.ListUsersDueForDigest(ctx, digestBatch)
	if err != nil {
		return err
	}
	var sent, quiet int
	for _, u := range due {
		since := time.Now().Add(-digestFirstWindow)
		if u.LastDigestAt.Valid {
			since = u.LastDigestAt.Time
		}
		rows, err := w.q.ListNotificationsSince(ctx, sqlc.ListNotificationsSinceParams{
			UserID: u.UserID,
			CreatedAt:   pgtype.Timestamptz{Time: since, Valid: true},
			Limit:       digestMaxItems,
		})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			// Nothing happened. Stamp anyway: a quiet account must not be
			// rescanned on every pass, and an empty digest is spam.
			if err := w.q.MarkUserDigestSent(ctx, u.UserID); err != nil {
				return err
			}
			quiet++
			continue
		}
		// Each user's own language when they picked one (users.locale, set
		// from the switcher); the deployment default otherwise.
		locale := w.digestLocale()
		if u.Locale != "" {
			locale = i18n.ParseOrDefault(u.Locale)
		}
		msg, err := mail.DigestMessage(locale, w.AppURL, u.Email, u.Name, digestItems(rows))
		if err != nil {
			return err
		}
		if err := w.sender.Send(ctx, msg); err != nil {
			return err // retried with backoff; unstamped users are still due
		}
		if err := w.q.MarkUserDigestSent(ctx, u.UserID); err != nil {
			return err
		}
		sent++
	}
	if sent > 0 || quiet > 0 {
		w.log.Info("email.digest", "sent", sent, "quiet", quiet, "due", len(due))
	}
	return nil
}

// digestItems maps notification rows to the email's view struct.
func digestItems(rows []sqlc.Notification) []templates.DigestItem {
	out := make([]templates.DigestItem, 0, len(rows))
	for _, n := range rows {
		out = append(out, templates.DigestItem{
			Title: n.Title,
			Body:  n.Body,
			URL:   n.Url,
			When:  n.CreatedAt.Time.UTC().Format("Jan 2, 15:04 MST"),
		})
	}
	return out
}
