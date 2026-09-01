package postgres

import (
	"context"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/notifications"
)

type Queue interface {
	Enqueue(context.Context, notifications.Message) error
}
type Notifier struct{ Queue Queue }

func New(q Queue) *Notifier { return &Notifier{Queue: q} }

var _ notifications.Notifier = (*Notifier)(nil)

func (n *Notifier) Send(c context.Context, o, u, k, t, b, url string) error {
	if n == nil || n.Queue == nil {
		return fmt.Errorf("notifications postgres: queue is required")
	}
	return n.Queue.Enqueue(c, notifications.Message{OrgID: o, UserID: u, Kind: k, Title: t, Body: b, URL: url})
}
func (n *Notifier) SendOrg(c context.Context, o, k, t, b, url string) error {
	return n.Send(c, o, "", k, t, b, url)
}

func (n *Notifier) Health(ctx context.Context) error {
	if n == nil || n.Queue == nil {
		return fmt.Errorf("notifications postgres: queue is required")
	}
	return nil
}

type Deps struct{}
type Module struct{ Value *Notifier }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	return &Module{Value: New(nil)}, ctx.Err()
}

func (m *Module) Health(ctx context.Context) error { return m.Value.Health(ctx) }
