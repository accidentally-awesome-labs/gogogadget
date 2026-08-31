// Package jobs is the Postgres-backed background worker. The claim query uses
// FOR UPDATE SKIP LOCKED with a 5-minute visibility timeout, so multiple
// worker processes can poll the same table safely.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/mail"
	"github.com/gogogadget/gogogadget/internal/notify"
	"github.com/gogogadget/gogogadget/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/text/language"
)

// Job kinds.
const (
	KindWelcome              = "email.welcome"
	KindPaymentFailed        = "email.payment_failed"
	KindSubscriptionCanceled = "email.subscription_canceled"
	KindTrialEnding          = "email.trial_ending"
	KindDunningReminder      = "email.dunning_reminder"
	KindDunningFinal         = "email.dunning_final"
	KindEmailDigest          = "email.digest" // per-user rollup of in-app notifications (digest.go)
	KindUsageFlush           = "usage.flush"  // usage metering (see internal/usage)
	KindWebhookDeliver       = "webhook.deliver"
	KindExportProjectsCSV    = "export.projects_csv"
	KindExportOrgJSON        = "export.org_json"
)

// SchedulableKindsContains reports whether kind may back a schedule row. The
// list itself is generated from module declarations — a kind cannot be scheduled
// unless its module said it may be, because a schedule pointing at a one-shot
// handler would fire it forever.
func SchedulableKindsContains(kind string) bool {
	for _, k := range SchedulableKinds {
		if k == kind {
			return true
		}
	}
	return false
}

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
	// The declaring module's budget is applied here because the row is dispatch
	// truth: a job keeps the budget it was enqueued under even if the module
	// later changes its mind. An undeclared kind gets the column default — a
	// project may queue work before the module that handles it is installed.
	_, err = q.EnqueueJob(ctx, sqlc.EnqueueJobParams{
		Kind: kind, Payload: raw, RunAt: at, MaxAttempts: int32(declaredAttempts[kind]),
	})
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
	// definitions is the dispatch table, keyed by kind. Built at construction
	// from the generated declarations; a kind no installed module declares is
	// absent, which is what makes a stale queued row dead-letter immediately.
	definitions map[string]Definition
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
	// AppURL is the base for links inside digest emails. The digest is the
	// one mail the worker renders itself (its content is a query result that
	// only exists at send time), so it needs the base URL that enqueue-time
	// builders normally supply.
	AppURL string
	// DigestLocale picks the catalog for digest emails. Users have no
	// per-user locale column yet (see /docs/roadmap), so this is a
	// deployment-wide choice; the zero value is English.
	DigestLocale language.Tag
}

// digestLocale normalizes the zero value to English.
func (w *Worker) digestLocale() language.Tag {
	if w.DigestLocale == language.Und {
		return language.English
	}
	return w.DigestLocale
}

func NewWorker(q *sqlc.Queries, sender mail.Sender, log *slog.Logger) *Worker {
	w := &Worker{q: q, sender: sender, log: log, poll: 2 * time.Second,
		WebhookGuard: guardWebhookURL, WebhookTransport: guardedTransport()}
	// Built once from the generated table. The declarations close over w, so the
	// collaborators callers assign after construction are still picked up.
	w.definitions = make(map[string]Definition, len(workerDefinitions(w)))
	for _, d := range workerDefinitions(w) {
		if d.ProviderActive != nil && !d.ProviderActive() { continue }
		w.definitions[d.Kind] = d
	}
	return w
}

// Run polls until ctx is done; a janitor pass deletes finished jobs older than
// 7 days and webhook events older than 30 days.
//
// The first pass runs immediately, and that is load-bearing rather than
// cosmetic. A ticker alone put the first sweep 24 hours after PROCESS START and
// reset the clock on every restart, so any deployment that recycles more often
// than daily - a daily container recycle, a normal deploy cadence, a
// crash-restart loop - never reached the janitor at all. Every failure was
// silent: finished jobs accumulated forever, the inbound webhook idempotency
// table grew without bound, AUDIT_RETENTION_DAYS was never enforced, expired
// idempotency keys were never swept, and rotated webhook secrets were never
// cleared past their grace window. Retention that only holds on a
// long-uptime host is not retention.
func (w *Worker) Run(ctx context.Context) {
	w.janitorPass(ctx)

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

		n := w.pass(ctx)
		if n == 0 {
			w.sleep(ctx, w.jittered())
		}
	}
}

// pass runs one claim-and-schedule cycle and is the last place a panic can be
// contained. dispatchSafely covers a panicking handler and janitorPass covers a
// panicking sweep, but the claim itself sat outside both: a panic anywhere in
// ClaimJob, CompleteJob, FailJob or schedulerPass unwound straight out of the
// goroutine Module.Start launched, with no recover above it, and took the web
// server down with it. That is the exact failure dispatchSafely exists to
// prevent, one frame further out - and the one the queue cannot answer by
// failing a row, because it never claimed one.
//
// Reporting zero work on a panic is what makes the next iteration sleep for a
// poll interval instead of spinning: a queue that cannot be reached at all must
// back off, not busy-loop on the failure.
func (w *Worker) pass(ctx context.Context) (n int) {
	defer func() {
		if r := recover(); r != nil {
			w.log.Error("job worker panicked", "panic", r, "stack", string(debug.Stack()))
			n = 0
		}
	}()

	n, err := w.drain(ctx)
	if err != nil {
		w.log.Error("job worker", "error", err)
	}
	if err := w.schedulerPass(ctx); err != nil {
		w.log.Error("schedule pass", "error", err)
	}
	return n
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

// Janitor is one declared cleanup sweep. Name is what the operator sees in the
type Janitor struct {
	Name  string
	Sweep func(context.Context) error
	ProviderActive func() bool
}

// janitorPass runs every declared sweep. Each is logged independently and a
// failure does not stop the others: an unreachable table must not strand the
// cleanup of every other one.
func (w *Worker) janitorPass(ctx context.Context) {
	// A sweep that panics must not end the claim loop. The passes are
	// independent maintenance, and losing retention is a much smaller failure
	// than losing the worker.
	defer func() {
		if r := recover(); r != nil {
			w.log.Error("janitor panicked", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	w.runJanitors(ctx, workerJanitors(w))
}

func (w *Worker) runJanitors(ctx context.Context, janitors []Janitor) {
	for _, janitor := range janitors {
		if janitor.ProviderActive != nil && !janitor.ProviderActive() { continue }
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					w.log.Error("janitor panic", "name", janitor.Name, "panic", recovered)
				}
			}()
			if err := janitor.Sweep(ctx); err != nil { w.log.Error("janitor", "name", janitor.Name, "error", err) }
		}()
	}
}
func (w *Worker) janitorOldJobs(ctx context.Context) error {
	return w.q.DeleteOldJobs(ctx)
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

	if err := w.dispatchSafely(ctx, job); err != nil {
		w.log.Error("job failed", "id", job.ID, "kind", job.Kind, "attempts", job.Attempts, "error", err)

		// An uninstalled module's queued rows die on the first claim. Retrying a
		// handler that cannot exist would burn the full backoff schedule — hours
		// of queue capacity — and bury the real signal behind repeated failures.
		reason := deadLetterReasonExhausted
		terminal := job.Attempts >= job.MaxAttempts
		if errors.Is(err, errUnknownKind) {
			reason, terminal = deadLetterReasonUninstalled, true
		}

		if terminal {
			if derr := w.q.DeadLetterJob(ctx, sqlc.DeadLetterJobParams{
				ID: job.ID, Reason: pgtype.Text{String: reason, Valid: true},
			}); derr != nil {
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

// dispatchSafely turns a panicking handler into a failed job. Without it a
// single bad handler takes down the whole process: Run is started in a goroutine
// by Module.Start with no recover above it, so the panic unwinds past the web
// server too. A background worker must not be able to kill the thing serving
// traffic, and the queue already has the right answer for a handler that cannot
// complete - fail it, back off, and dead-letter at the attempt budget.
//
// The stack is logged because a recovered panic with no stack is a bug report
// with the evidence removed.
func (w *Worker) dispatchSafely(ctx context.Context, job sqlc.Job) (err error) {
	defer func() {
		if r := recover(); r != nil {
			w.log.Error("job handler panicked", "id", job.ID, "kind", job.Kind,
				"panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("handler panicked: %v", r)
		}
	}()
	return w.dispatch(ctx, job)
}

// schedulerPass claims due schedules (next_run_at advanced in the same
// statement — missed ticks are skipped by design) and enqueues their kind
// with the wrapped payload. Runs every poll cycle.
//
// A failing enqueue logs and continues rather than returning. The claim already
// advanced next_run_at for EVERY row it returned, so returning on the first
// error abandoned the rest: five due schedules with a transient error on the
// third meant two fired, three did not, and all five had their tick consumed.
// One failure taking out its unrelated siblings is the same mistake the janitor
// pass was corrected for.
//
// The remaining hole is honest and stated: the claim and the enqueue are not one
// transaction, so a process killed between them loses that tick with no record.
// The design comment above covers skipping ticks during DOWNTIME, which is a
// different thing from a claim whose enqueue never happened.
func (w *Worker) schedulerPass(ctx context.Context) error {
	due, err := w.q.ClaimDueSchedules(ctx)
	if err != nil {
		return err
	}
	var failed int
	for _, s := range due {
		if err := Enqueue(ctx, w.q, s.Kind, SchedulePayload{ScheduleID: s.ID, Payload: s.Payload}); err != nil {
			failed++
			w.log.Error("schedule enqueue", "schedule_id", s.ID, "kind", s.Kind, "error", err)
		}
	}
	if failed > 0 {
		return fmt.Errorf("enqueued %d of %d due schedules", len(due)-failed, len(due))
	}
	return nil
}

// deadLetterReasonExhausted and deadLetterReasonUninstalled are the terminal
// reasons a job can carry. They are literals shared with the admin status view
// and the requeue guard, so they are named once here.
const (
	deadLetterReasonExhausted   = "exhausted"
	deadLetterReasonUninstalled = "module_uninstalled"
)

// errUnknownKind marks a persisted row whose kind no installed module provides.
// It is not a handler failure, so it never retries.
var errUnknownKind = errors.New("no installed module provides this job kind")

// sendTransactionalEmail is the shared body behind every email kind. The kind is
// a parameter rather than a branch on the job row, because each kind is now its
// own declaration and the row is no longer in scope here.
func (w *Worker) sendTransactionalEmail(ctx context.Context, kind string, p EmailPayload) error {
	if kind == KindTrialEnding {
		if skip, err := w.trialNoLongerActive(ctx, p.OrgID); err != nil {
			return err
		} else if skip {
			w.log.Info("trial-ending email skipped: subscription no longer trialing", "org", p.OrgID)
			return nil
		}
	}
	if kind == KindDunningReminder || kind == KindDunningFinal {
		if skip, err := w.paymentRecovered(ctx, p.OrgID); err != nil {
			return err
		} else if skip {
			w.log.Info("dunning email skipped: payment no longer failing", "kind", kind, "org", p.OrgID)
			return nil
		}
		if kind == KindDunningFinal {
			// The day-0 notification is a week stale by now; the last
			// notice is the one worth putting back in front of them.
			notify.SendOrg(ctx, w.q, p.OrgID, "payment_failed", "Final notice: payment still failing",
				"Update your card to keep your plan active.", "/app/settings/billing")
		}
	}
	return w.sender.Send(ctx, mail.Message{To: p.To, Subject: p.Subject, HTML: p.HTML, Text: p.Text})
}

// dispatch routes a claimed row to the declaration that owns its kind. The table
// is generated from module manifests, so an uninstalled module's kind is simply
// absent — which ProcessOne turns into an immediate dead-letter rather than a
// retry of a handler that cannot exist.
func (w *Worker) dispatch(ctx context.Context, job sqlc.Job) error {
	definition, ok := w.definitions[job.Kind]
	if !ok {
		return fmt.Errorf("%w: %s", errUnknownKind, job.Kind)
	}
	// Attempt state comes from the row, not the declaration: a job keeps the
	// budget it was enqueued under even if the module later changes its mind.
	return definition.Handle(ctx, job.Payload, Attempt{
		Number: int(job.Attempts), Max: int(job.MaxAttempts),
	})
}

// paymentRecovered is the run-time guard for the dunning follow-ups. They are
// scheduled days ahead at the moment of failure, so by send time the customer
// may well have fixed their card — and few things burn goodwill faster than a
// "your payment is failing" email arriving after it succeeded. Anything other
// than a still-past_due subscription (recovered, canceled, or gone) skips.
func (w *Worker) paymentRecovered(ctx context.Context, orgID string) (bool, error) {
	if orgID == "" {
		return true, nil
	}
	sub, err := w.q.GetSubscriptionByOrg(ctx, orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return sub.Status != "past_due", nil
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
