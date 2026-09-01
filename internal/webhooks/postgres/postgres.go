package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/webhooks"
	"github.com/jackc/pgx/v5/pgconn"
)

type DB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Ping(context.Context) error
}
type Emitter struct{ DB DB }

func New(db DB) *Emitter { return &Emitter{DB: db} }
func (e *Emitter) Emit(c context.Context, org, typ string, data any) error {
	if e == nil || e.DB == nil {
		return fmt.Errorf("webhooks postgres: database is required")
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = e.DB.Exec(c, `INSERT INTO webhook_outbox (org_id,event_type,payload) VALUES ($1,$2,$3)`, org, typ, payload)
	return err
}

var _ webhooks.Emitter = (*Emitter)(nil)

func (e *Emitter) Health(ctx context.Context) error {
	if e == nil || e.DB == nil {
		return fmt.Errorf("webhooks postgres: database is required")
	}
	return e.DB.Ping(ctx)
}

type Deps struct{ Pool DB }
type Module struct{ Value *Emitter }

func NewModule(ctx context.Context, _ apphost.Host, d Deps) (*Module, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Pool == nil {
		return nil, fmt.Errorf("webhooks postgres: database is required")
	}
	return &Module{Value: New(d.Pool)}, nil
}
func (m *Module) Health(ctx context.Context) error {
	if m == nil || m.Value == nil {
		return fmt.Errorf("webhooks postgres: emitter is required")
	}
	return m.Value.Health(ctx)
}
