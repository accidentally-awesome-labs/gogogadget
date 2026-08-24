// Package-level module wiring. Deps/NewModule is the uniform constructor shape
// the generated bootstrap calls.
package ratelimit

import (
	"context"
	"fmt"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
)

// defaultPerMinute mirrors the configuration default, so a limiter built from an
// invalid budget behaves like an unconfigured one instead of refusing to boot.
const defaultPerMinute = 100

// Deps is the typed dependency set the generated bootstrap supplies.
type Deps struct {
	Config *config.Config
}

// Module is the constructed rate-limit closure. The limiter holds per-key state
// and a process-lifetime janitor, so it is a live object rather than a function.
type Module struct {
	Limiter *Keyed
}

// NewModule builds the per-IP limiter from the configured budget. Load profiles
// differ, so the budget is configuration rather than a constant.
func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if d.Config == nil {
		return nil, fmt.Errorf("rate limit: config dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	budget := d.Config.RateLimitPerMinute
	if budget <= 0 {
		// A non-positive budget would divide by zero building the rate; fall back
		// to the documented default rather than refusing to boot over a typo.
		budget = defaultPerMinute
		h.Log().Warn("rate limit: budget unset or invalid, using default", "per_minute", budget)
	}
	return &Module{Limiter: PerMinute(budget)}, nil
}
