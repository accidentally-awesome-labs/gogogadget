package noop

import (
	"context"
	"github.com/gogogadget/gogogadget/internal/analytics"
)

type Capturer = analytics.NoopCapturer
type Module struct{ Capturer Capturer }
type Deps struct{}

func NewModule(ctx context.Context, _ any, _ Deps) (*Module, error) { return &Module{}, ctx.Err() }
func (m *Module) Health(ctx context.Context) error                  { return ctx.Err() }

var _ analytics.Capturer = Capturer{}
