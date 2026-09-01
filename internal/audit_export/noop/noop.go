package noop

import (
	"context"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/audit"
)

type Exporter struct{}

func New() *Exporter                                        { return &Exporter{} }
func (*Exporter) Export(context.Context, audit.Entry) error { return nil }

var _ audit.Exporter = (*Exporter)(nil)

func (*Exporter) Health(ctx context.Context) error { return ctx.Err() }

type Deps struct{}
type Module struct{ Value *Exporter }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	return &Module{Value: New()}, ctx.Err()
}

func (m *Module) Health(ctx context.Context) error { return ctx.Err() }
