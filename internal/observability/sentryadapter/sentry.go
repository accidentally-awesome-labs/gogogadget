package sentryadapter

import (
	"context"
	"fmt"
	"github.com/getsentry/sentry-go"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/observability"
)

type Module struct {
	Value  observability.Reporter
	client *sentry.Client
}
type Deps struct{ DSN, Environment string }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.DSN == "" {
		d.DSN = h.Env("SENTRY_DSN")
	}
	if d.Environment == "" {
		d.Environment = h.Env("APP_ENV")
	}
	if d.DSN == "" {
		return nil, fmt.Errorf("sentry: DSN is required")
	}
	c, err := sentry.NewClient(sentry.ClientOptions{Dsn: d.DSN, Environment: d.Environment})
	if err != nil {
		return nil, err
	}
	return &Module{Value: observability.NewSentryReporter(c), client: c}, nil
}
func (m *Module) Stop(ctx context.Context) error {
	if m == nil || m.client == nil {
		return nil
	}
	done := make(chan struct{})
	go func() { m.client.FlushWithContext(context.Background()); m.client.Close(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *Module) Health(ctx context.Context) error {
	if m == nil || m.client == nil {
		return fmt.Errorf("sentry: client is required")
	}
	return nil
}
