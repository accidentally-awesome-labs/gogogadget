// Package-level module wiring. Deps/NewModule is the uniform constructor shape
// the generated bootstrap calls.
package analytics

import (
	"context"
	"fmt"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
)

// Deps is the typed dependency set the generated bootstrap supplies.
type Deps struct {
	Config *config.Config
}

// Module is the constructed analytics closure. Capturer is always live so every
// call site can fire and forget.
type Module struct {
	Capturer Capturer
}

// NewModule selects PostHog when configured and the no-op capturer otherwise.
func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if d.Config == nil {
		return nil, fmt.Errorf("analytics: config dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !d.Config.PostHogEnabled() {
		return &Module{Capturer: NoopCapturer{}}, nil
	}
	ph, err := NewPostHog(d.Config.PostHogAPIKey, d.Config.PostHogHost)
	if err != nil {
		return nil, fmt.Errorf("posthog init: %w", err)
	}
	h.Log().Info("analytics: posthog", "host", d.Config.PostHogHost)
	return &Module{Capturer: ph}, nil
}
