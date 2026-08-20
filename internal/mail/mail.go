// Package mail is the Sender seam: handlers and job workers never import an
// email SDK directly. Swapping Resend for another provider means replacing
// one file.
package mail

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/resendlabs/resend-go"
	"golang.org/x/text/language"
)

type Message struct {
	To, Subject, HTML, Text string
}

// Sender delivers one fully-rendered message.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// ResendSender sends via the Resend API.
type ResendSender struct {
	client *resend.Client
	from   string
}

func NewResendSender(apiKey, from string) *ResendSender {
	return &ResendSender{client: resend.NewClient(apiKey), from: from}
}

// Send delivers via the Resend API. resend-go has no context support, so ctx
// is accepted for the interface and not forwarded.
func (s *ResendSender) Send(_ context.Context, msg Message) error {
	_, err := s.client.Emails.Send(&resend.SendEmailRequest{
		From:    s.from,
		To:      []string{msg.To},
		Subject: msg.Subject,
		Html:    msg.HTML,
		Text:    msg.Text,
	})
	return err
}

// DevSender is the zero-infra default: logs the subject/recipient AND writes
// the rendered HTML to tmp/emails/ so emails are viewable in a browser.
type DevSender struct {
	log *slog.Logger
	dir string
}

func NewDevSender(log *slog.Logger, dir string) *DevSender {
	return &DevSender{log: log, dir: dir}
}

func (s *DevSender) Send(ctx context.Context, msg Message) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	name := fmt.Sprintf("%d-%s.html", time.Now().UnixNano(), sanitizeFilename(msg.To))
	path := filepath.Join(s.dir, name)
	if err := os.WriteFile(path, []byte(msg.HTML), 0o644); err != nil {
		return err
	}
	s.log.Info("dev email", "to", msg.To, "subject", msg.Subject, "file", path)
	return nil
}

func sanitizeFilename(s string) string {
	mapped := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			return r
		default:
			return '_'
		}
	}, s)
	// Dots are kept (email domains read naturally: new_example.com) but dot
	// runs are collapsed so the result can never carry a "../" traversal.
	for strings.Contains(mapped, "..") {
		mapped = strings.ReplaceAll(mapped, "..", ".")
	}
	return mapped
}

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
