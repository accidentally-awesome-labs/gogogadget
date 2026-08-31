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

type Deps struct{ Config *config.Config }
type Module struct{ Sender mail.Sender }

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
func (m *Module) Health(ctx context.Context) error {
	if sender, ok := m.Sender.(apphost.HealthChecker); ok {
		return sender.Health(ctx)
	}
	return fmt.Errorf("mail resend: sender does not implement health")
}

type ResendSender struct {
	client *resendgo.Client
	from   string
}

func NewResendSender(apiKey, from string) *ResendSender {
	return &ResendSender{client: resendgo.NewClient(apiKey), from: from}
}
func (s *ResendSender) Send(ctx context.Context, msg mail.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := s.client.Emails.Send(&resendgo.SendEmailRequest{From: s.from, To: []string{msg.To}, Subject: msg.Subject, Html: msg.HTML, Text: msg.Text})
	return err
}
func (s *ResendSender) Health(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.client == nil || s.client.Domains == nil {
		return fmt.Errorf("resend client is nil")
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.client.Domains.List()
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

var _ mail.Sender = (*ResendSender)(nil)
var _ apphost.HealthChecker = (*ResendSender)(nil)
