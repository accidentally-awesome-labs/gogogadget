// Package resend implements the managed Resend mail adapter.
package resend

import (
	"context"
	"fmt"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/mail"
	resendgo "github.com/resendlabs/resend-go"
)

type Deps struct {
	Config *config.Config
}

type Module struct {
	Sender mail.Sender
}

func NewModule(ctx context.Context, _ apphost.Host, d Deps) (*Module, error) {
	if d.Config == nil {
		return nil, fmt.Errorf("mail resend: config dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Config.ResendAPIKey == "" {
		return nil, fmt.Errorf("mail resend: RESEND_API_KEY is required")
	}
	return &Module{Sender: NewResendSender(d.Config.ResendAPIKey, d.Config.EmailFrom)}, nil
}

type ResendSender struct {
	client *resendgo.Client
	from   string
}

func NewResendSender(apiKey, from string) *ResendSender {
	return &ResendSender{client: resendgo.NewClient(apiKey), from: from}
}

func (s *ResendSender) Send(_ context.Context, msg mail.Message) error {
	_, err := s.client.Emails.Send(&resendgo.SendEmailRequest{
		From:    s.from,
		To:      []string{msg.To},
		Subject: msg.Subject,
		Html:    msg.HTML,
		Text:    msg.Text,
	})
	return err
}

var _ mail.Sender = (*ResendSender)(nil)
