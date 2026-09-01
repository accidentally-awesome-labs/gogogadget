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
		return err
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

type Deps struct{}
type Module struct{ Value *Recorder }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	return &Module{Value: New(nil, nil, nil)}, ctx.Err()
}

func (m *Module) Health(ctx context.Context) error { return m.Value.Health(ctx) }
