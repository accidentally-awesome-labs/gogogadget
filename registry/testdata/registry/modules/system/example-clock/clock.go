// Package clock is the provider-style system module of the example closure in
// registry/testdata. It exists to exercise the generated typed DAG end to end:
// the manifest declares a package, a constructor, one typed need it takes from a
// production capability, one narrow port it provides, and both lifecycle hooks.
// The generated bootstrap therefore emits a direct constructor call, a Runtime
// field of the declared type, and start/stop registrations — so a wrong
// declaration is a compile error on a named generated line rather than a
// service that silently never boots.
package clock

import (
	"context"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
)

// Clock is the narrow port this module provides. A port rather than a struct
// pointer, so a consumer depends on the behaviour and not on this module's
// internals.
type Clock interface {
	// Now is the host clock plus the configured skew.
	Now() time.Time
}

// Deps is exactly this module's declared needs, field for field. The generated
// bootstrap builds this literal, so a need the manifest forgot to declare is a
// missing field at compile time.
type Deps struct {
	Config *config.Config
}

// Module is exactly this module's declared provides, plus the lifecycle methods
// the manifest says it has.
type Module struct {
	Clock Clock

	host    apphost.Host
	skew    time.Duration
	started bool
}

// New constructs the module. The skew comes from the environment key this
// module declares, which lands on config.Config through
// internal/config/config_registry_gen.go — so reading it here proves the
// generated configuration field and the manifest declaration are the same fact.
func New(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m := &Module{host: h}
	if d.Config != nil {
		m.skew = time.Duration(d.Config.ExampleClockSkewSeconds) * time.Second
	}
	m.Clock = m
	return m, nil
}

// Now implements Clock.
func (m *Module) Now() time.Time { return m.host.Now().Add(m.skew) }

// Start is the declared long-lived hook. It must return promptly, so it only
// records that the runtime reached it.
func (m *Module) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.started = true
	return nil
}

// Stop is the declared shutdown hook. It is idempotent and honours context
// expiry, which is the contract apphost.Stop states.
func (m *Module) Stop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.started = false
	return nil
}
