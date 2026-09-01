// Package postgres adapts PostgreSQL LISTEN/NOTIFY to realtime.Broker.
package postgres

import (
	"context"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/realtime"
)

type Listener interface {
	Listen(context.Context, string) (Subscription, error)
	Notify(context.Context, string, []byte) error
}
type Subscription interface {
	Next(context.Context) ([]byte, error)
	Close() error
}
type Broker struct{ db Listener }

func New(db Listener) *Broker { return &Broker{db: db} }

var _ realtime.Broker = (*Broker)(nil)

func (b *Broker) Publish(ctx context.Context, t string, p []byte) error {
	if b.db == nil {
		return fmt.Errorf("postgres realtime: listener is required")
	}
	return b.db.Notify(ctx, t, p)
}
func (b *Broker) Subscribe(ctx context.Context, t string) (realtime.Subscription, error) {
	if b.db == nil {
		return nil, fmt.Errorf("postgres realtime: listener is required")
	}
	s, err := b.db.Listen(ctx, t)
	if err != nil {
		return nil, err
	}
	return subscription{s}, nil
}

type subscription struct{ s Subscription }

func (x subscription) Next(ctx context.Context) ([]byte, error) { return x.s.Next(ctx) }
func (x subscription) Close() error                             { return x.s.Close() }

func (b *Broker) Health(ctx context.Context) error {
	if b == nil || b.db == nil {
		return fmt.Errorf("postgres realtime: listener is required")
	}
	return nil
}
