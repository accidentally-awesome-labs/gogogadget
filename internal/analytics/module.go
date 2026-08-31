// Package-level module wiring. Deps/NewModule is the uniform constructor shape
// the generated bootstrap calls.
package analytics

import (
	"context"
	"fmt"
	"sync"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
)

// Deps is the typed dependency set the generated bootstrap supplies.
type Deps struct {
	Config *config.Config
}

// Module is the constructed analytics closure. Capturer is always live so every
// call site can fire and forget. If the selected capturer buffers events, the
// module owns its shutdown.
type Module struct {
	Capturer Capturer

	closeOnce sync.Once
	closed    chan struct{}
}

var _ apphost.Lifecycle = (*Module)(nil)

// NewModule selects PostHog when configured and the no-op capturer otherwise.
func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if d.Config == nil {
		return nil, fmt.Errorf("analytics: config dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m := &Module{Capturer: NoopCapturer{}, closed: make(chan struct{})}
	if !d.Config.PostHogEnabled() {
		return m, nil
	}
	ph, err := NewPostHog(d.Config.PostHogAPIKey, d.Config.PostHogHost)
	if err != nil {
		return nil, fmt.Errorf("posthog init: %w", err)
	}
	h.Log().Info("analytics: posthog", "host", d.Config.PostHogHost)
	m.Capturer = ph
	return m, nil
}

// Stop closes a buffering capturer once and waits for it to flush. Waiting is
// bounded by ctx, while the close operation is allowed to finish in the
// background so a timed-out caller cannot trigger a second close.
func (m *Module) Stop(ctx context.Context) error {
	if m == nil || m.Capturer == nil {
		return nil
	}
	m.closeOnce.Do(func() {
		go func() {
			if c, ok := m.Capturer.(BufferingCapturer); ok {
				c.Close()
			}
			close(m.closed)
		}()
	})
	select {
	case <-m.closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
