package billing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/notify"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// SubscriptionPayload is the webhook `data` for all subscription.* events.
type SubscriptionPayload struct {
	ID                string    `json:"id"`
	Status            string    `json:"status"`
	CurrentPeriodEnd  time.Time `json:"current_period_end"`
	CancelAtPeriodEnd bool      `json:"cancel_at_period_end"`
	CustomerID        string    `json:"customer_id"`
	ProductID         string    `json:"product_id"`
	TrialEnd          time.Time `json:"trial_end"`
	Customer          struct {
		ExternalID string `json:"external_id"`
	} `json:"customer"`
	Metadata map[string]string `json:"metadata"`
}

// OrgID resolves the local org: checkout metadata first, then the Polar
// customer's external id (both are the org_id by construction).
func (p SubscriptionPayload) OrgID() string {
	if id := p.Metadata["org_id"]; id != "" {
		return id
	}
	return p.Customer.ExternalID
}

// EmailSink enqueues billing emails (rendered and scheduled by the web
// layer). billing never imports mail/jobs — that would create an import
// cycle through templates.
type EmailSink interface {
	EnqueueTrialEnding(ctx context.Context, to string, trialEnd time.Time, orgID string) error
	EnqueuePaymentFailed(ctx context.Context, to string, orgID string) error
	EnqueueSubscriptionCanceled(ctx context.Context, to string, periodEnd time.Time, orgID string) error
	// EnqueueDunning schedules a follow-up for a payment that is still
	// failing. Both are sent into the future and both re-check the
	// subscription before delivering.
	EnqueueDunning(ctx context.Context, stage, to string, periodEnd time.Time, orgID string, runAt time.Time) error
}

// Dunning schedule: how long after the first failure each follow-up goes out.
//
// The payment processor runs its own card retries; this is the human half —
// the reminders that get someone to update an expired card before the
// subscription dies. Two follow-ups is the boilerplate default because a
// third rarely converts and starts to read as harassment. Change the
// constants to change the cadence; nothing else knows the numbers.
const (
	DunningReminderAfter = 72 * time.Hour  // day 3: "we are still retrying"
	DunningFinalAfter    = 168 * time.Hour // day 7: last notice before cancellation
)

// Dunning stages, used as the email kind suffix and in logs.
const (
	DunningReminder = "reminder"
	DunningFinal    = "final"
)

// Processor is the subscription state machine: webhook event → row, audit,
// email job, analytics. Constructed per-request by the webhook handler.
type Processor struct {
	Q            *sqlc.Queries
	Log          *slog.Logger
	ProductPlans map[string]string // provider product ID → plan key
	Provider     string
	Emails       EmailSink
	// Capture reports analytics events (nil = no-op until observability lands).
	Capture func(userID, event string, props map[string]any)
}

// ProcessSubscription handles one verified subscription.* event.
// Returning nil → 200; error → 500 so the provider retries. Unknown products
// are logged and ACKed (200) so a stale product never wedges the endpoint.
//
// Provider is required rather than defaulted: it is written to
// billing_accounts.provider, and a seam that guesses one adapter's name
// attributes another adapter's rows to it.
func (p *Processor) ProcessSubscription(ctx context.Context, eventType string, sub SubscriptionPayload) error {
	orgID := sub.OrgID()
	provider := p.Provider
	if provider == "" {
		return fmt.Errorf("billing: subscription event carries no provider")
	}
	if orgID == "" {
		p.Log.Warn("billing webhook: no org reference", "type", eventType, "sub", sub.ID)
		return nil
	}
	productKey, ok := p.ProductPlans[sub.ProductID]
	if !ok {
		p.Log.Warn("billing webhook: unknown product id (ignored)", "product", sub.ProductID, "type", eventType)
		return nil
	}

	if sub.CustomerID != "" {
		account, accountErr := p.Q.GetBillingAccount(ctx, sqlc.GetBillingAccountParams{Provider: provider, ProviderCustomerID: sub.CustomerID})
		if accountErr == nil && account.OrgID != orgID {
			return fmt.Errorf("billing customer %q is already linked to another organization", sub.CustomerID)
		}
		if accountErr != nil && !errors.Is(accountErr, pgx.ErrNoRows) {
			return accountErr
		}
		if errors.Is(accountErr, pgx.ErrNoRows) {
			if _, insertErr := p.Q.InsertBillingAccount(ctx, sqlc.InsertBillingAccountParams{Provider: provider, ProviderCustomerID: sub.CustomerID, OrgID: orgID}); insertErr != nil {
				// A concurrent insert is safe only when it resolves to this org.
				account, rereadErr := p.Q.GetBillingAccount(ctx, sqlc.GetBillingAccountParams{Provider: provider, ProviderCustomerID: sub.CustomerID})
				if rereadErr != nil || account.OrgID != orgID {
					return fmt.Errorf("billing customer %q could not be linked", sub.CustomerID)
				}
			}
		}
	}

	// Read BEFORE upsert: transitions drive emails and audit.
	prev, prevErr := p.Q.GetSubscriptionByOrg(ctx, orgID)
	if prevErr != nil && !errors.Is(prevErr, pgx.ErrNoRows) {
		return prevErr
	}
	prevStatus := ""
	if prevErr == nil {
		prevStatus = prev.Status
	}

	periodEnd := pgtype.Timestamptz{Time: sub.CurrentPeriodEnd, Valid: !sub.CurrentPeriodEnd.IsZero()}
	cancelAtEnd := sub.CancelAtPeriodEnd
	switch eventType {
	case "subscription.active", "subscription.uncanceled":
		cancelAtEnd = false
	case "subscription.canceled":
		cancelAtEnd = true
	}

	if _, err := p.Q.UpsertSubscription(ctx, sqlc.UpsertSubscriptionParams{
		OrgID:                  orgID,
		Provider:               provider,
		ProviderSubscriptionID: pgtype.Text{String: sub.ID, Valid: true},
		ProviderCustomerID:     sub.CustomerID,
		ProductKey:             productKey,
		Status:                 sub.Status,
		CurrentPeriodEnd:       periodEnd,
		CancelAtPeriodEnd:      cancelAtEnd,
	}); err != nil {
		return err
	}

	ownerEmail, err := p.ownerEmail(ctx, orgID)
	if err != nil {
		p.Log.Warn("billing webhook: owner lookup failed (emails skipped)", "org", orgID, "error", err)
		ownerEmail = ""
	}

	switch eventType {
	case "subscription.created":
		if sub.Status == "trialing" && ownerEmail != "" && p.Emails != nil {
			if err := p.Emails.EnqueueTrialEnding(ctx, ownerEmail, sub.TrialEnd, orgID); err != nil {
				return err
			}
		}

	case "subscription.updated":
		if sub.Status == "past_due" && prevStatus != "past_due" {
			// In-app first, and outside the email gate: it needs no address,
			// so an org whose owner lookup failed still learns that its card
			// is failing instead of silently sliding toward cancellation.
			notify.SendOrg(ctx, p.Q, orgID, "payment_failed", "Payment failed",
				"We couldn't charge your card — your plan stays active while we retry.", "/app/settings/billing")
		}
		if sub.Status == "past_due" && prevStatus != "past_due" && ownerEmail != "" && p.Emails != nil {
			if err := p.Emails.EnqueuePaymentFailed(ctx, ownerEmail, orgID); err != nil {
				return err
			}

			// …and the follow-up sequence. Scheduled now, guarded later: each
			// job re-reads the subscription before sending, so a customer who
			// fixes their card tomorrow never receives the day-7 warning.
			now := time.Now()
			for stage, after := range map[string]time.Duration{
				DunningReminder: DunningReminderAfter,
				DunningFinal:    DunningFinalAfter,
			} {
				if err := p.Emails.EnqueueDunning(ctx, stage, ownerEmail, sub.CurrentPeriodEnd, orgID, now.Add(after)); err != nil {
					return err
				}
			}
		}
		if sub.Status != prevStatus {
			audit.Log(ctx, p.Q, orgID, "", "subscription.updated", map[string]any{"from": prevStatus, "to": sub.Status})
		}

	case "subscription.active":
		audit.Log(ctx, p.Q, orgID, "", "subscription.activated", map[string]any{"plan": productKey})
		if p.Capture != nil {
			p.Capture(orgID, "subscription_activated", map[string]any{"plan": productKey})
		}

	case "subscription.canceled", "subscription.revoked":
		if prevStatus != "canceled" && ownerEmail != "" && p.Emails != nil {
			if err := p.Emails.EnqueueSubscriptionCanceled(ctx, ownerEmail, sub.CurrentPeriodEnd, orgID); err != nil {
				return err
			}
		}
		action := "subscription.canceled"
		if eventType == "subscription.revoked" {
			action = "subscription.revoked"
		}
		audit.Log(ctx, p.Q, orgID, "", action, map[string]any{"plan": productKey})
		if p.Capture != nil {
			p.Capture(orgID, "subscription_canceled", map[string]any{"plan": productKey, "via": eventType})
		}

	case "subscription.uncanceled":
		// Customer withdrew cancellation in the portal — without this branch
		// they would lose access at period end while paying.
		audit.Log(ctx, p.Q, orgID, "", "subscription.reactivated", map[string]any{"plan": productKey})

	default:
		p.Log.Info("billing webhook: unhandled event (ignored)", "type", eventType)
	}
	return nil
}

// ownerEmail picks a recipient for org-level email: the first org:admin, else
// the first member.
func (p *Processor) ownerEmail(ctx context.Context, orgID string) (string, error) {
	members, err := p.Q.ListMembersByOrg(ctx, orgID)
	if err != nil {
		return "", err
	}
	for _, m := range members {
		if m.Role == "org:admin" {
			return string(m.Email), nil
		}
	}
	if len(members) > 0 {
		return string(members[0].Email), nil
	}
	return "", fmt.Errorf("org %s has no members", orgID)
}
