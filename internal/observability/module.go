// Package-level module wiring. Deps/NewModule is the uniform constructor shape
// the generated bootstrap calls.
package observability

import (
	"context"
	"fmt"
	"sync"

	"github.com/getsentry/sentry-go"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
)

// Deps is the typed dependency set the generated bootstrap supplies.
type Deps struct {
	Config *config.Config
}

// Module is the constructed reporting closure. Reporter is always live so call
// sites never nil-check. A configured module owns its Sentry client and flushes
// it during shutdown.
type Module struct {
	Reporter Reporter
	client   *sentry.Client

	stopOnce sync.Once
	stopped  chan struct{}
}

var _ apphost.Lifecycle = (*Module)(nil)

// NewModule selects Sentry when a DSN is configured and the no-op reporter
// otherwise.
func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if d.Config == nil {
		return nil, fmt.Errorf("observability: config dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m := &Module{Reporter: NoopReporter{}, stopped: make(chan struct{})}
	if !d.Config.SentryEnabled() {
		return m, nil
	}
	client, err := sentry.NewClient(sentry.ClientOptions{
		Dsn:         d.Config.SentryDSN,
		Environment: d.Config.Env,
	})
	if err != nil {
		return nil, fmt.Errorf("sentry init: %w", err)
	}
	h.Log().Info("observability: sentry")
	m.client = client
	m.Reporter = NewSentryReporter(client)
	return m, nil
}

// Stop flushes and closes the module-owned Sentry client once. The flush and
// close use a detached context so a caller that times out can retry with a
// fresh context while the in-flight shutdown completes.
func (m *Module) Stop(ctx context.Context) error {
	if m == nil || m.client == nil {
		return nil
	}
	m.stopOnce.Do(func() {
		go func() {
			_ = m.client.FlushWithContext(context.Background())
			m.client.Close()
			close(m.stopped)
		}()
	})
	select {
	case <-m.stopped:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
