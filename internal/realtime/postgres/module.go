package postgres

import (
	"context"
	"fmt"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/realtime"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Deps struct{ Pool *pgxpool.Pool }
type Module struct{ Value *Broker }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Pool == nil {
		return nil, fmt.Errorf("postgres realtime: pool is required")
	}
	v := New(poolListener{pool: d.Pool})
	return &Module{Value: v}, nil
}

func (m *Module) Health(ctx context.Context) error {
	if m == nil || m.Value == nil {
		return fmt.Errorf("postgres realtime: broker is required")
	}
	return m.Value.Health(ctx)
}

type poolListener struct{ pool *pgxpool.Pool }

func (l poolListener) Notify(ctx context.Context, topic string, payload []byte) error {
	if l.pool == nil {
		return fmt.Errorf("postgres realtime: pool is required")
	}
	_, err := l.pool.Exec(ctx, `SELECT pg_notify($1, $2)`, topic, string(payload))
	return err
}

func (l poolListener) Listen(ctx context.Context, topic string) (Subscription, error) {
	if l.pool == nil {
		return nil, fmt.Errorf("postgres realtime: pool is required")
	}
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, `LISTEN `+pgx.Identifier{topic}.Sanitize()); err != nil {
		conn.Release()
		return nil, err
	}
	return &poolSubscription{conn: conn}, nil
}

type poolSubscription struct{ conn *pgxpool.Conn }

func (s *poolSubscription) Next(ctx context.Context) ([]byte, error) {
	if s == nil || s.conn == nil {
		return nil, realtime.ErrClosed
	}
	n, err := s.conn.Conn().WaitForNotification(ctx)
	if err != nil {
		return nil, err
	}
	return []byte(n.Payload), nil
}
func (s *poolSubscription) Close() error {
	if s == nil || s.conn == nil {
		return nil
	}
	s.conn.Release()
	s.conn = nil
	return nil
}
