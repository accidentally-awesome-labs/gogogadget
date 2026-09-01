package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/usage"
)

type Queries interface {
	InsertUsageEvent(context.Context, sqlc.InsertUsageEventParams) (sqlc.UsageEvent, error)
}
type Recorder struct{ Q Queries }

func New(q Queries) *Recorder { return &Recorder{Q: q} }
func (r *Recorder) Record(c context.Context, o, n string, v int64, e string, m map[string]any) error {
	if r == nil || r.Q == nil {
		return fmt.Errorf("usage postgres: queries are required")
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = r.Q.InsertUsageEvent(c, sqlc.InsertUsageEventParams{OrgID: o, Name: n, Value: v, ExternalID: e, Metadata: raw})
	return err
}

var _ usage.Recorder = (*Recorder)(nil)

func (r *Recorder) Health(ctx context.Context) error {
	if r == nil || r.Q == nil {
		return fmt.Errorf("usage postgres: queries are required")
	}
	return nil
}

type Deps struct{}
type Module struct{ Value *Recorder }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	return &Module{Value: New(nil)}, ctx.Err()
}

func (m *Module) Health(ctx context.Context) error { return m.Value.Health(ctx) }
