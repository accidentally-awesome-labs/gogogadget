// Package jobs is the Postgres-backed background worker. The claim query uses
// FOR UPDATE SKIP LOCKED with a 5-minute visibility timeout, so multiple
// worker processes can poll the same table safely.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/mail"
	"github.com/gogogadget/gogogadget/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Job kinds.
const (
	KindWelcome              = "email.welcome"
	KindPaymentFailed        = "email.payment_failed"
	KindSubscriptionCanceled = "email.subscription_canceled"
	KindTrialEnding          = "email.trial_ending"
	KindEmailDigest          = "email.digest" // seeded example — no-ops until a builder replaces it
	KindUsageFlush           = "usage.flush"  // usage metering (see internal/usage)
	KindWebhookDeliver       = "webhook.deliver"
	KindExportProjectsCSV    = "export.projects_csv"
)

// ExportProjectsPayload is the handler-enqueued CSV export contract.
type ExportProjectsPayload struct {
	OrgID  string `json:"org_id"`
	UserID string `json:"user_id"`
}

// SchedulePayload wraps every schedule-enqueued job: the scheduler pass
// writes it, and dispatch cases that accept scheduled work unwrap .Payload.
type SchedulePayload struct {
	ScheduleID int64           `json:"schedule_id"`
	Payload    json.RawMessage `json:"payload"`
}

// EmailPayload is the job payload contract for all email kinds: rendered
// bodies at enqueue time, so workers never touch templates.
type EmailPayload struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	HTML    string `json:"html"`
	Text    string `json:"text"`
	OrgID   string `json:"org_id,omitempty"`
}

// Enqueue inserts a job to run immediately.
func Enqueue(ctx context.Context, q *sqlc.Queries, kind string, payload any) error {
	return EnqueueAt(ctx, q, kind, payload, time.Time{})
}

// EnqueueAt inserts a job to run at a specific time (zero = now).
func EnqueueAt(ctx context.Context, q *sqlc.Queries, kind string, payload any, runAt time.Time) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var at pgtype.Timestamptz
	if !runAt.IsZero() {
		at = pgtype.Timestamptz{Time: runAt, Valid: true}
	}
	_, err = q.EnqueueJob(ctx, sqlc.EnqueueJobParams{Kind: kind, Payload: raw, RunAt: at})
	return err
}

// EnqueueEmail renders nothing — callers pass a built mail.Message and the
// payload carries it verbatim.
func EnqueueEmail(ctx context.Context, q *sqlc.Queries, kind string, msg mail.Message, orgID string, runAt time.Time) error {
	return EnqueueAt(ctx, q, kind, EmailPayload{
		To: msg.To, Subject: msg.Subject, HTML: msg.HTML, Text: msg.Text, OrgID: orgID,
	}, runAt)
}

// Worker claims and dispatches jobs until its context is canceled.
type Worker struct {
	q      *sqlc.Queries
	sender mail.Sender
	log    *slog.Logger
	poll   time.Duration
	// OnDeadLetter reports exhausted jobs (wired to Sentry when enabled).
	OnDeadLetter func(kind string, err error)
	// Webhook delivery policy hooks — strict by default; tests swap these.
	WebhookGuard     func(ctx context.Context, rawURL string) error
	WebhookTransport *http.Transport
	// Billing is the usage-flush target: nil (unconfigured) → flush no-ops
	// and events stay local. Set in cmd/server when Polar is configured.
	Billing billing.Client
	// AuditRetentionDays > 0 makes the janitor delete audit rows older than
	// that many days (AUDIT_RETENTION_DAYS). 0 = retain forever.
	AuditRetentionDays int
	// Storage is the export target; set in cmd/server. Nil → export fails
	// loudly (the handler should not enqueue without it).
	Storage storage.Store
}

func NewWorker(q *sqlc.Queries, sender mail.Sender, log *slog.Logger) *Worker {
	return &Worker{q: q, sender: sender, log: log, poll: 2 * time.Second,
		WebhookGuard: guardWebhookURL, WebhookTransport: guardedTransport()}
}

// Run polls until ctx is done; a daily janitor pass deletes finished jobs
// older than 7 days and webhook events older than 30 days.
func (w *Worker) Run(ctx context.Context) {
	janitor := time.NewTicker(24 * time.Hour)
	defer janitor.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-janitor.C:
			w.janitorPass(ctx)
		default:
		}

		n, err := w.drain(ctx)
		if err != nil {
			w.log.Error("job worker", "error", err)
		}
		if err := w.schedulerPass(ctx); err != nil {
			w.log.Error("schedule pass", "error", err)
		}
		if n == 0 {
			w.sleep(ctx, w.jittered())
		}
	}
}

func (w *Worker) jittered() time.Duration {
	return w.poll + time.Duration(rand.Int64N(int64(w.poll/2)))
}

func (w *Worker) sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func (w *Worker) janitorPass(ctx context.Context) {
	if err := w.q.DeleteOldJobs(ctx); err != nil {
		w.log.Error("janitor jobs", "error", err)
	}
	if err := w.q.DeleteOldWebhookEvents(ctx); err != nil {
		w.log.Error("janitor webhook_events", "error", err)
	}
	if w.AuditRetentionDays > 0 {
		cutoff := pgtype.Timestamptz{Time: time.Now().AddDate(0, 0, -w.AuditRetentionDays), Valid: true}
		n, err := w.q.DeleteOldAuditRows(ctx, cutoff)
		if err != nil {
			w.log.Error("janitor audit_log", "error", err)
		} else if n > 0 {
			w.log.Info("janitor audit_log", "deleted", n, "retention_days", w.AuditRetentionDays)
		}
	}
}

// drain processes every currently-claimable job; returns the count.
func (w *Worker) drain(ctx context.Context) (int, error) {
	n := 0
	for {
		done, err := w.ProcessOne(ctx)
		if err != nil {
			return n, err
		}
		if !done {
			return n, nil
		}
		n++
	}
}

// ProcessOne claims and runs a single job. Reports whether work happened.
func (w *Worker) ProcessOne(ctx context.Context) (bool, error) {
	job, err := w.q.ClaimJob(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	if err := w.dispatch(ctx, job); err != nil {
		w.log.Error("job failed", "id", job.ID, "kind", job.Kind, "attempts", job.Attempts, "error", err)
		if job.Attempts >= job.MaxAttempts {
			if derr := w.q.DeadLetterJob(ctx, job.ID); derr != nil {
				return true, derr
			}
			if w.OnDeadLetter != nil {
				w.OnDeadLetter(job.Kind, err)
			}
			return true, nil
		}
		if ferr := w.q.FailJob(ctx, sqlc.FailJobParams{ID: job.ID, LastError: pgtype.Text{String: err.Error(), Valid: true}}); ferr != nil {
			return true, ferr
		}
		return true, nil
	}
	return true, w.q.CompleteJob(ctx, job.ID)
}

// schedulerPass claims due schedules (next_run_at advanced in the same
// statement — missed ticks are skipped by design) and enqueues their kind
// with the wrapped payload. Runs every poll cycle.
func (w *Worker) schedulerPass(ctx context.Context) error {
	due, err := w.q.ClaimDueSchedules(ctx)
	if err != nil {
		return err
	}
	for _, s := range due {
		if err := Enqueue(ctx, w.q, s.Kind, SchedulePayload{ScheduleID: s.ID, Payload: s.Payload}); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) dispatch(ctx context.Context, job sqlc.Job) error {
	switch job.Kind {
	case KindEmailDigest:
		// Placeholder for builders: scheduled example job; no-ops politely
		// with a log when the payload carries nothing to digest.
		var sp SchedulePayload
		if err := json.Unmarshal(job.Payload, &sp); err != nil {
			return err
		}
		w.log.Info("email.digest: placeholder — nothing to send", "schedule_id", sp.ScheduleID)
		return nil
	case KindUsageFlush:
		return w.flushUsage(ctx, job)
	case KindWebhookDeliver:
		return w.deliverWebhook(ctx, job)
	case KindExportProjectsCSV:
		return w.exportProjectsCSV(ctx, job)
	case KindWelcome, KindPaymentFailed, KindSubscriptionCanceled, KindTrialEnding:
		var p EmailPayload
		if err := json.Unmarshal(job.Payload, &p); err != nil {
			return err
		}
		if job.Kind == KindTrialEnding {
			if skip, err := w.trialNoLongerActive(ctx, p.OrgID); err != nil {
				return err
			} else if skip {
				w.log.Info("trial-ending email skipped: subscription no longer trialing", "org", p.OrgID)
				return nil
			}
		}
		return w.sender.Send(ctx, mail.Message{To: p.To, Subject: p.Subject, HTML: p.HTML, Text: p.Text})
	default:
		return errors.New("unknown job kind: " + job.Kind)
	}
}

// trialNoLongerActive is the run-time guard for email.trial_ending: the row
// was enqueued at subscription.created — if the trial has since converted or
// died, sending "your trial ends soon" would be wrong.
func (w *Worker) trialNoLongerActive(ctx context.Context, orgID string) (bool, error) {
	if orgID == "" {
		return false, nil
	}
	sub, err := w.q.GetSubscriptionByOrg(ctx, orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil // subscription row gone → nothing to remind about
	}
	if err != nil {
		return false, err
	}
	return sub.Status != "trialing", nil
}
