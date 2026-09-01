package log

import (
	"context"
	"github.com/gogogadget/gogogadget/internal/observability"
	"log/slog"
)

type Reporter = observability.NoopReporter
type Module struct{ Value Reporter }
type Deps struct{}

func NewModule(ctx context.Context, h interface{ Log() *slog.Logger }, _ Deps) (*Module, error) {
	_ = h
	return &Module{}, ctx.Err()
}
func (m *Module) Health(ctx context.Context) error { return ctx.Err() }

var _ observability.Reporter = Reporter{}
