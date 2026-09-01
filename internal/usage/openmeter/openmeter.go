// Package openmeter queues usage events before managed delivery.
package openmeter

import (
	"context"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/usage"
)

type Queue interface {
	Enqueue(context.Context, string, string, int64, string, map[string]any) error
}
type Delivery func(context.Context, string, string, int64, string, map[string]any) error
type Recorder struct {
	Queue   Queue
	Deliver Delivery
	Report  func(context.Context, error)
}

func New(q Queue, d Delivery, report func(context.Context, error)) *Recorder {
	return &Recorder{Queue: q, Deliver: d, Report: report}
}
func (r *Recorder) Record(c context.Context, o, n string, v int64, e string, m map[string]any) error {
	if r == nil || r.Queue == nil {
		return fmt.Errorf("openmeter: queue is required")
	}
	if err := r.Queue.Enqueue(c, o, n, v, e, m); err != nil {
		if r.Report != nil {
			r.Report(c, err)
		}
		// Usage is recorded by the product transaction; forwarding is
		// eventually consistent and must not fail that transaction.
		return nil
	}
	if r.Deliver != nil {
		if err := r.Deliver(c, o, n, v, e, m); err != nil && r.Report != nil {
			r.Report(c, err)
		}
	}
	return nil
}

var _ usage.Recorder = (*Recorder)(nil)

func (r *Recorder) Health(ctx context.Context) error {
	if r == nil || r.Queue == nil {
		return fmt.Errorf("openmeter: queue is required")
	}
	return nil
}

type Deps struct{ Queue Queue }
type Module struct{ Value *Recorder }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Queue == nil {
		return nil, fmt.Errorf("openmeter: queue is required")
	}
	return &Module{Value: New(d.Queue, nil, nil)}, nil
}

func (m *Module) Health(ctx context.Context) error { return m.Value.Health(ctx) }
