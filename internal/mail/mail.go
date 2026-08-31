// Package mail is the constructor-free Sender seam. Handlers and job workers
// depend on these provider-neutral types; concrete adapters live in sibling
// packages under internal/mail.
package mail

import (
	"context"
	"strings"

	"github.com/a-h/templ"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"golang.org/x/text/language"
)

type Message struct {
	To, Subject, HTML, Text string
}

// Sender delivers one fully-rendered message.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// Message builders and the Sender contract intentionally live in this seam;
// adapter packages only implement Sender.

// --- Message builders: render the templ components to strings at enqueue
// time, so job payloads carry rendered bodies and workers never touch templates.
// locale picks the catalog (zero value = English); bodies and subjects are
// localized through i18n.T on the render context.

func renderHTML(ctx context.Context, c templ.Component) (string, error) {
	var b strings.Builder
	if err := c.Render(ctx, &b); err != nil {
		return "", err
	}
	return b.String(), nil
}

func WelcomeMessage(locale language.Tag, appURL, to, name string) (Message, error) {
	ctx := i18n.WithTag(context.Background(), locale)
	html, err := renderHTML(ctx, templates.WelcomeEmailHTML(appURL, name))
	if err != nil {
		return Message{}, err
	}
	text, err := renderHTML(ctx, templates.WelcomeEmailText(appURL, name))
	if err != nil {
		return Message{}, err
	}
	return Message{To: to, Subject: i18n.T(ctx, "email.welcome.subject"), HTML: html, Text: text}, nil
}

func PaymentFailedMessage(locale language.Tag, appURL, to string) (Message, error) {
	ctx := i18n.WithTag(context.Background(), locale)
	html, err := renderHTML(ctx, templates.PaymentFailedEmailHTML(appURL))
	if err != nil {
		return Message{}, err
	}
	text, err := renderHTML(ctx, templates.PaymentFailedEmailText(appURL))
	if err != nil {
		return Message{}, err
	}
	return Message{To: to, Subject: i18n.T(ctx, "email.payment_failed.subject"), HTML: html, Text: text}, nil
}

func SubscriptionCanceledMessage(locale language.Tag, appURL, to, periodEnd string) (Message, error) {
	ctx := i18n.WithTag(context.Background(), locale)
	html, err := renderHTML(ctx, templates.SubscriptionCanceledEmailHTML(appURL, periodEnd))
	if err != nil {
		return Message{}, err
	}
	text, err := renderHTML(ctx, templates.SubscriptionCanceledEmailText(appURL, periodEnd))
	if err != nil {
		return Message{}, err
	}
	return Message{To: to, Subject: i18n.T(ctx, "email.canceled.subject"), HTML: html, Text: text}, nil
}

func TrialEndingMessage(locale language.Tag, appURL, to, trialEnd string) (Message, error) {
	ctx := i18n.WithTag(context.Background(), locale)
	html, err := renderHTML(ctx, templates.TrialEndingEmailHTML(appURL, trialEnd))
	if err != nil {
		return Message{}, err
	}
	text, err := renderHTML(ctx, templates.TrialEndingEmailText(appURL, trialEnd))
	if err != nil {
		return Message{}, err
	}
	return Message{To: to, Subject: i18n.T(ctx, "email.trial_ending.subject"), HTML: html, Text: text}, nil
}

// DigestMessage renders the periodic rollup. Unlike the transactional
// builders it is called from the worker rather than at enqueue time: the
// content is a query result that only exists when the digest actually runs,
// so rendering early would mean storing a stale email in a job payload.
func DigestMessage(locale language.Tag, appURL, to, name string, items []templates.DigestItem) (Message, error) {
	ctx := i18n.WithTag(context.Background(), locale)
	html, err := renderHTML(ctx, templates.DigestEmailHTML(appURL, name, items))
	if err != nil {
		return Message{}, err
	}
	text, err := renderHTML(ctx, templates.DigestEmailText(appURL, name, items))
	if err != nil {
		return Message{}, err
	}
	return Message{To: to, Subject: i18n.T(ctx, "email.digest.subject"), HTML: html, Text: text}, nil
}

// DunningReminderMessage is the day-3 nudge: the card is still failing.
func DunningReminderMessage(locale language.Tag, appURL, to string) (Message, error) {
	ctx := i18n.WithTag(context.Background(), locale)
	html, err := renderHTML(ctx, templates.DunningReminderEmailHTML(appURL))
	if err != nil {
		return Message{}, err
	}
	text, err := renderHTML(ctx, templates.DunningReminderEmailText(appURL))
	if err != nil {
		return Message{}, err
	}
	return Message{To: to, Subject: i18n.T(ctx, "email.dunning_reminder.subject"), HTML: html, Text: text}, nil
}

// DunningFinalMessage is the last notice before the subscription lapses.
func DunningFinalMessage(locale language.Tag, appURL, to, periodEnd string) (Message, error) {
	ctx := i18n.WithTag(context.Background(), locale)
	html, err := renderHTML(ctx, templates.DunningFinalEmailHTML(appURL, periodEnd))
	if err != nil {
		return Message{}, err
	}
	text, err := renderHTML(ctx, templates.DunningFinalEmailText(appURL, periodEnd))
	if err != nil {
		return Message{}, err
	}
	return Message{To: to, Subject: i18n.T(ctx, "email.dunning_final.subject"), HTML: html, Text: text}, nil
}
