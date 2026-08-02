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
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/resendlabs/resend-go"
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
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			return r
		default:
			return '_'
		}
	}, s)
}

// --- Message builders: render the templ components to strings at enqueue
// time, so job payloads carry rendered bodies and workers never touch templates.

func renderHTML(c templ.Component) (string, error) {
	var b strings.Builder
	if err := c.Render(context.Background(), &b); err != nil {
		return "", err
	}
	return b.String(), nil
}

func WelcomeMessage(appURL, to, name string) (Message, error) {
	html, err := renderHTML(templates.WelcomeEmailHTML(appURL, name))
	if err != nil {
		return Message{}, err
	}
	text, err := renderHTML(templates.WelcomeEmailText(appURL, name))
	if err != nil {
		return Message{}, err
	}
	return Message{To: to, Subject: "Welcome to GoGoGadget", HTML: html, Text: text}, nil
}

func PaymentFailedMessage(appURL, to string) (Message, error) {
	html, err := renderHTML(templates.PaymentFailedEmailHTML(appURL))
	if err != nil {
		return Message{}, err
	}
	text, err := renderHTML(templates.PaymentFailedEmailText(appURL))
	if err != nil {
		return Message{}, err
	}
	return Message{To: to, Subject: "Your payment failed", HTML: html, Text: text}, nil
}

func SubscriptionCanceledMessage(appURL, to, periodEnd string) (Message, error) {
	html, err := renderHTML(templates.SubscriptionCanceledEmailHTML(appURL, periodEnd))
	if err != nil {
		return Message{}, err
	}
	text, err := renderHTML(templates.SubscriptionCanceledEmailText(appURL, periodEnd))
	if err != nil {
		return Message{}, err
	}
	return Message{To: to, Subject: "Your subscription is canceled", HTML: html, Text: text}, nil
}

func TrialEndingMessage(appURL, to, trialEnd string) (Message, error) {
	html, err := renderHTML(templates.TrialEndingEmailHTML(appURL, trialEnd))
	if err != nil {
		return Message{}, err
	}
	text, err := renderHTML(templates.TrialEndingEmailText(appURL, trialEnd))
	if err != nil {
		return Message{}, err
	}
	return Message{To: to, Subject: "Your trial ends soon", HTML: html, Text: text}, nil
}

