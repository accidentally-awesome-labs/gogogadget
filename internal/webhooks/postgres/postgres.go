package postgres

import (
	"context"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/webhooks"
)

type Queue interface {
	Enqueue(context.Context, string, string, any) error
}
type Emitter struct{ Queue Queue }

func New(q Queue) *Emitter { return &Emitter{Queue: q} }
func (e *Emitter) Emit(c context.Context, o, t string, d any) error {
	if e == nil || e.Queue == nil {
		return fmt.Errorf("webhooks postgres: queue is required")
	}
	return e.Queue.Enqueue(c, o, t, d)
}

var _ webhooks.Emitter = (*Emitter)(nil)

func (e *Emitter) Health(ctx context.Context) error {
	if e == nil || e.Queue == nil {
		return fmt.Errorf("webhooks postgres: queue is required")
	}
	return nil
}

type Deps struct{}
type Module struct{ Value *Emitter }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	return &Module{Value: New(nil)}, ctx.Err()
}

func (m *Module) Health(ctx context.Context) error { return m.Value.Health(ctx) }
