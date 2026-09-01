// Package launchdarkly is a read-only feature flag adapter. Mutations are
// intentionally rejected; operators use the provider console.
package launchdarkly

import (
	"context"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/flags"
)

type Service struct {
	ProviderConsole string
	EnabledFn       func(context.Context, string, string) bool
	ListFn          func(context.Context) ([]flags.Flag, error)
	OverridesFn     func(context.Context, string) ([]flags.Override, error)
}

func New(console string, enabled func(context.Context, string, string) bool, list func(context.Context) ([]flags.Flag, error), overrides func(context.Context, string) ([]flags.Override, error)) *Service {
	return &Service{ProviderConsole: console, EnabledFn: enabled, ListFn: list, OverridesFn: overrides}
}
func (s *Service) Enabled(ctx context.Context, o, k string) bool {
	if s == nil || s.EnabledFn == nil {
		return false
	}
	return s.EnabledFn(ctx, o, k)
}
func (s *Service) List(ctx context.Context) ([]flags.Flag, error) {
	if s == nil || s.ListFn == nil {
		return nil, fmt.Errorf("launchdarkly: list client is required")
	}
	return s.ListFn(ctx)
}
func (s *Service) Upsert(context.Context, flags.Flag) error { return flags.ErrReadOnly }
func (s *Service) Delete(context.Context, string) error     { return flags.ErrReadOnly }
func (s *Service) ListOverrides(ctx context.Context, k string) ([]flags.Override, error) {
	if s == nil || s.OverridesFn == nil {
		return nil, fmt.Errorf("launchdarkly: overrides client is required")
	}
	return s.OverridesFn(ctx, k)
}
func (s *Service) SetOverride(context.Context, string, string, bool) error { return flags.ErrReadOnly }
func (s *Service) DeleteOverride(context.Context, string, string) error    { return flags.ErrReadOnly }

func (s *Service) Health(ctx context.Context) error {
	if s == nil || s.EnabledFn == nil {
		return fmt.Errorf("launchdarkly: client is required")
	}
	return nil
}

type Deps struct{}
type Module struct{ Value *Service }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	return &Module{Value: New(h.Env("LAUNCHDARKLY_CONSOLE"), nil, nil, nil)}, ctx.Err()
}

func (m *Module) Health(ctx context.Context) error { return m.Value.Health(ctx) }
