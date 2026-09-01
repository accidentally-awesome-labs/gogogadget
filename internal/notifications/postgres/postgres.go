package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/notifications"
	"github.com/jackc/pgx/v5"
)

type Queue interface {
	Enqueue(context.Context, notifications.Message) error
}
type queryQueue struct{ q *sqlc.Queries }

func (q queryQueue) Enqueue(ctx context.Context, m notifications.Message) error {
	if q.q == nil {
		return fmt.Errorf("notifications postgres: queries are required")
	}
	if m.UserID != "" {
		pref, err := q.q.GetNotificationPreference(ctx, sqlc.GetNotificationPreferenceParams{UserID: m.UserID, Kind: m.Kind})
		if err == nil && !pref.InApp {
			return nil
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	_, err := q.q.InsertNotification(ctx, sqlc.InsertNotificationParams{OrgID: m.OrgID, UserID: m.UserID, Kind: m.Kind, Title: m.Title, Body: m.Body, Url: m.URL})
	return err
}
func (q queryQueue) Fanout(ctx context.Context, m notifications.Message) error {
	if q.q == nil {
		return fmt.Errorf("notifications postgres: queries are required")
	}
	members, err := q.q.ListMembersByOrg(ctx, m.OrgID)
	if err != nil {
		return err
	}
	for _, member := range members {
		m.UserID = member.UserID
		if err := q.Enqueue(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

type Notifier struct {
	Queue       Queue
	FanoutQueue interface {
		Fanout(context.Context, notifications.Message) error
	}
}

func New(q Queue) *Notifier {
	n := &Notifier{Queue: q}
	if fq, ok := q.(interface {
		Fanout(context.Context, notifications.Message) error
	}); ok {
		n.FanoutQueue = fq
	}
	return n
}

var _ notifications.Notifier = (*Notifier)(nil)

func (n *Notifier) Send(c context.Context, o, u, k, t, b, url string) error {
	if n == nil || n.Queue == nil {
		return fmt.Errorf("notifications postgres: queue is required")
	}
	return n.Queue.Enqueue(c, notifications.Message{OrgID: o, UserID: u, Kind: k, Title: t, Body: b, URL: url})
}
func (n *Notifier) SendOrg(c context.Context, o, k, t, b, url string) error {
	if n == nil {
		return fmt.Errorf("notifications postgres: notifier is required")
	}
	m := notifications.Message{OrgID: o, Kind: k, Title: t, Body: b, URL: url}
	if n.FanoutQueue != nil {
		return n.FanoutQueue.Fanout(c, m)
	}
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
