package web

import (
	"context"
	"fmt"
	"time"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/jobs"
	"github.com/gogogadget/gogogadget/internal/mail"
	"golang.org/x/text/language"
)

// emailSink implements billing.EmailSink: renders the templ email pair and
// enqueues the matching job kind. billing stays import-cycle-free.
type emailSink struct {
	s *Server
}

func (e emailSink) EnqueueTrialEnding(ctx context.Context, to string, trialEnd time.Time, orgID string) error {
	// Billing emails stay English this round (documented in /docs/i18n).
	msg, err := mail.TrialEndingMessage(language.English, e.s.cfg.AppURL, to, trialEnd.Format("January 2, 2006"))
	if err != nil {
		return err
	}
	// Three days before trial end.
	return jobs.EnqueueEmail(ctx, e.s.q, jobs.KindTrialEnding, msg, orgID, trialEnd.Add(-3*24*time.Hour))
}

func (e emailSink) EnqueuePaymentFailed(ctx context.Context, to string, orgID string) error {
	msg, err := mail.PaymentFailedMessage(language.English, e.s.cfg.AppURL, to)
	if err != nil {
		return err
	}
	return jobs.EnqueueEmail(ctx, e.s.q, jobs.KindPaymentFailed, msg, orgID, time.Time{})
}

func (e emailSink) EnqueueSubscriptionCanceled(ctx context.Context, to string, periodEnd time.Time, orgID string) error {
	periodEndStr := ""
	if !periodEnd.IsZero() {
		periodEndStr = periodEnd.Format("January 2, 2006")
	}
	msg, err := mail.SubscriptionCanceledMessage(language.English, e.s.cfg.AppURL, to, periodEndStr)
	if err != nil {
		return err
	}
	return jobs.EnqueueEmail(ctx, e.s.q, jobs.KindSubscriptionCanceled, msg, orgID, time.Time{})
}

var _ billing.EmailSink = emailSink{}

// EnqueueDunning schedules one follow-up in the dunning sequence. The body is
// rendered now (like every other billing email) but the job carries a future
// run_at; the worker re-checks the subscription before it sends.
func (e emailSink) EnqueueDunning(ctx context.Context, stage, to string, periodEnd time.Time, orgID string, runAt time.Time) error {
	var (
		msg  mail.Message
		kind string
		err  error
	)
	switch stage {
	case billing.DunningReminder:
		kind = jobs.KindDunningReminder
		msg, err = mail.DunningReminderMessage(language.English, e.s.cfg.AppURL, to)
	case billing.DunningFinal:
		kind = jobs.KindDunningFinal
		periodEndStr := ""
		if !periodEnd.IsZero() {
			periodEndStr = periodEnd.Format("January 2, 2006")
		}
		msg, err = mail.DunningFinalMessage(language.English, e.s.cfg.AppURL, to, periodEndStr)
	default:
		return fmt.Errorf("unknown dunning stage %q", stage)
	}
	if err != nil {
		return err
	}
	return jobs.EnqueueEmail(ctx, e.s.q, kind, msg, orgID, runAt)
}
