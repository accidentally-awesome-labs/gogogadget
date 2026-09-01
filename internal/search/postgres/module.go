package postgres

import (
	"context"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/search"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Deps struct{ Pool *pgxpool.Pool }
type Module struct{ Value *Index }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Pool == nil {
		return nil, fmt.Errorf("search postgres: pool is required")
	}
	return &Module{Value: New(d.Pool)}, nil
}
func (m *Module) Health(ctx context.Context) error {
	if m == nil || m.Value == nil {
		return fmt.Errorf("search postgres: index is required")
	}
	return m.Value.Health(ctx)
}

var _ search.Index = (*Index)(nil)
