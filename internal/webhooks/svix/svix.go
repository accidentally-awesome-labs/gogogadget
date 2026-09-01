// Package svix queues outbound events before delivering them to Svix.
package svix

import (
	"context"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/webhooks"
)

type Queue interface {
	Enqueue(context.Context, string, string, any) error
}
type Delivery func(context.Context, string, string, any) error
type Emitter struct {
	Queue   Queue
	Deliver Delivery
	Report  func(context.Context, error)
}

func New(q Queue, d Delivery, report func(context.Context, error)) *Emitter {
	return &Emitter{Queue: q, Deliver: d, Report: report}
}
func (e *Emitter) Emit(c context.Context, o, t string, d any) error {
	if e == nil || e.Queue == nil {
		return fmt.Errorf("svix: queue is required")
	}
	if err := e.Queue.Enqueue(c, o, t, d); err != nil {
		if e.Report != nil {
			e.Report(c, err)
		}
		return err
	}
	if e.Deliver != nil {
		if err := e.Deliver(c, o, t, d); err != nil && e.Report != nil {
			e.Report(c, err)
		}
	}
	return nil
}

var _ webhooks.Emitter = (*Emitter)(nil)

func (e *Emitter) Health(ctx context.Context) error {
	if e == nil || e.Queue == nil {
		return fmt.Errorf("svix: queue is required")
	}
	return nil
}

type Deps struct{}
type Module struct{ Value *Emitter }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	return &Module{Value: New(nil, nil, nil)}, ctx.Err()
}

func (m *Module) Health(ctx context.Context) error { return m.Value.Health(ctx) }
