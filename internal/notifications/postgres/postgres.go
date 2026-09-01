package postgres

import (
	"context"
	"fmt"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/notifications"
)

type Queue interface {
	Enqueue(context.Context, notifications.Message) error
}
type queryQueue struct{ q *sqlc.Queries }

func (q queryQueue) Enqueue(ctx context.Context, m notifications.Message) error {
	if q.q == nil {
		return fmt.Errorf("notifications postgres: queries are required")
	}
	_, err := q.q.InsertNotification(ctx, sqlc.InsertNotificationParams{OrgID: m.OrgID, UserID: m.UserID, Kind: m.Kind, Title: m.Title, Body: m.Body, Url: m.URL})
	return err
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
	return ctx.Err()
}

type Deps struct{ Queries *sqlc.Queries }
type Module struct{ Value *Notifier }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Queries == nil {
		return nil, fmt.Errorf("notifications postgres: queries are required")
	}
	return &Module{Value: New(queryQueue{q: d.Queries})}, nil
}

func (m *Module) Health(ctx context.Context) error {
	if m == nil || m.Value == nil {
		return fmt.Errorf("notifications postgres: notifier is required")
	}
	return m.Value.Health(ctx)
}
