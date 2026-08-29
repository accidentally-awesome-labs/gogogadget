// Package-level module wiring. Deps/NewModule is the uniform constructor shape
// the generated bootstrap calls.
package observability

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

// Module is the constructed reporting closure. Reporter is always live so call
// sites never nil-check.
type Module struct {
	Reporter Reporter
}

// NewModule selects Sentry when a DSN is configured and the no-op reporter
// otherwise.
func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if d.Config == nil {
		return nil, fmt.Errorf("observability: config dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Config.SentryEnabled() {
		h.Log().Info("observability: sentry")
		return &Module{Reporter: NewSentryReporter()}, nil
	}
	return &Module{Reporter: NoopReporter{}}, nil
}
