package web

import (
	"context"
	"time"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/jobs"
	"github.com/gogogadget/gogogadget/internal/mail"
)

// emailSink implements billing.EmailSink: renders the templ email pair and
// enqueues the matching job kind. billing stays import-cycle-free.
type emailSink struct {
	s *Server
}

func (e emailSink) EnqueueTrialEnding(ctx context.Context, to string, trialEnd time.Time, orgID string) error {
	msg, err := mail.TrialEndingMessage(e.s.cfg.AppURL, to, trialEnd.Format("January 2, 2006"))
	if err != nil {
		return err
	}
	// Three days before trial end.
	return jobs.EnqueueEmail(ctx, e.s.q, jobs.KindTrialEnding, msg, orgID, trialEnd.Add(-3*24*time.Hour))
}

func (e emailSink) EnqueuePaymentFailed(ctx context.Context, to string, orgID string) error {
	msg, err := mail.PaymentFailedMessage(e.s.cfg.AppURL, to)
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
	msg, err := mail.SubscriptionCanceledMessage(e.s.cfg.AppURL, to, periodEndStr)
	if err != nil {
		return err
	}
	return jobs.EnqueueEmail(ctx, e.s.q, jobs.KindSubscriptionCanceled, msg, orgID, time.Time{})
}

var _ billing.EmailSink = emailSink{}
