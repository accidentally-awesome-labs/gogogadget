// Package knock is a managed notification adapter. Queue is always written
// before the optional network delivery function, preserving retry semantics.
package knock

import (
	"context"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/notifications"
)

type Queue interface {
	Enqueue(context.Context, notifications.Message) error
}
type Delivery func(context.Context, notifications.Message) error
type Notifier struct {
	Queue   Queue
	Deliver Delivery
	Report  func(context.Context, error)
}

func New(q Queue, d Delivery, report func(context.Context, error)) *Notifier {
	return &Notifier{Queue: q, Deliver: d, Report: report}
}
func (n *Notifier) send(c context.Context, m notifications.Message) error {
	if n == nil || n.Queue == nil {
		return fmt.Errorf("knock: queue is required")
	}
	if err := n.Queue.Enqueue(c, m); err != nil {
		if n.Report != nil {
			n.Report(c, err)
		}
		return err
	}
	if n.Deliver != nil {
		if err := n.Deliver(c, m); err != nil {
			if n.Report != nil {
				n.Report(c, err)
			}
			return nil
		}
	}
	return nil
}
func (n *Notifier) Send(c context.Context, o, u, k, t, b, url string) error {
	return n.send(c, notifications.Message{OrgID: o, UserID: u, Kind: k, Title: t, Body: b, URL: url})
}
func (n *Notifier) SendOrg(c context.Context, o, k, t, b, url string) error {
	return n.send(c, notifications.Message{OrgID: o, Kind: k, Title: t, Body: b, URL: url})
}

var _ notifications.Notifier = (*Notifier)(nil)

func (n *Notifier) Health(ctx context.Context) error {
	if n == nil || n.Queue == nil {
		return fmt.Errorf("knock: queue is required")
	}
	return nil
}

type Deps struct{}
type Module struct{ Value *Notifier }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	return &Module{Value: New(nil, nil, nil)}, ctx.Err()
}

func (m *Module) Health(ctx context.Context) error { return m.Value.Health(ctx) }
