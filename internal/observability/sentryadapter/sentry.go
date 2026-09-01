// Package sentryadapter contains all Sentry SDK integration. The observability
// seam remains vendor-free.
package sentryadapter

import (
	"context"
	"fmt"
	"net/http"

	"github.com/getsentry/sentry-go"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/observability"
)

type Reporter struct{ hub *sentry.Hub }

func newReporter(client *sentry.Client) *Reporter {
	return &Reporter{hub: sentry.NewHub(client, sentry.NewScope())}
}
func (r *Reporter) Capture(err error) {
	if r != nil && r.hub != nil {
		r.hub.CaptureException(err)
	}
}
func (r *Reporter) CaptureRequest(req *http.Request, err error) {
	if r == nil || r.hub == nil || req == nil {
		return
	}
	r.hub.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("path", req.URL.Path)
		scope.SetContext("request", sentry.Context{"url": req.URL.String(), "method": req.Method})
		r.hub.CaptureException(err)
	})
}

var _ observability.Reporter = (*Reporter)(nil)

type Module struct {
	Value  observability.Reporter
	client *sentry.Client
}
type Deps struct{ DSN, Environment string }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.DSN == "" && h != nil {
		d.DSN = h.Env("SENTRY_DSN")
	}
	if d.Environment == "" && h != nil {
		d.Environment = h.Env("APP_ENV")
	}
	if d.DSN == "" {
		return nil, fmt.Errorf("sentry: DSN is required")
	}
	c, err := sentry.NewClient(sentry.ClientOptions{Dsn: d.DSN, Environment: d.Environment})
	if err != nil {
		return nil, err
	}
	return &Module{Value: newReporter(c), client: c}, nil
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
	return ctx.Err()
}
