// Package-level module wiring. Deps/NewModule is the uniform constructor shape
// the generated bootstrap calls. Config is the root of the dependency graph:
// it needs nothing but the host, and every other module needs it.
package config

import (
	"context"
	"fmt"

	"github.com/gogogadget/gogogadget/internal/apphost"
)

// Deps is the typed dependency set the generated bootstrap supplies. Config
// reads only the host environment, so it has none.
type Deps struct{}

// Module is the constructed configuration closure.
type Module struct {
	Config *Config
}

// NewModule parses and validates configuration from the host environment. It
// never reads the process environment directly, so a runtime can boot against
// a fixed map in tests.
func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// `make dev` is documented to work from a fresh clone with only a copied
	// .env, so development fills the process environment from it before parsing.
	// A host backed by a fixed map never reads the process environment, so this
	// cannot leak into a test's view.
	switch h.Env("APP_ENV") {
	case "", "development":
		loadDotEnv(".env") // a missing file is fine; the real environment wins
	}
	cfg, err := LoadFrom(h.Env)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &Module{Config: &cfg}, nil
}
