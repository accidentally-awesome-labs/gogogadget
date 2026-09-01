// Package postgres is the sole database adapter. docker-postgres and neon are
// service targets of this same implementation.
package postgres

import (
	"context"
	"fmt"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/db"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Deps struct{ Config *config.Config }
type Module struct{ *db.Module }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	m, err := db.NewModule(ctx, h, db.Deps{Config: d.Config})
	if err != nil {
		return nil, err
	}
	return &Module{Module: m}, nil
}
func (m *Module) Health(ctx context.Context) error {
	if m == nil || m.Pool == nil {
		return fmt.Errorf("database: pool is required")
	}
	return m.Pool.Ping(ctx)
}
func Pool(m *Module) *pgxpool.Pool {
	if m == nil {
		return nil
	}
	return m.Pool
}
func Queries(m *Module) *sqlc.Queries {
	if m == nil {
		return nil
	}
	return m.Queries
}

var _ apphost.Lifecycle = (*Module)(nil)
var _ apphost.HealthChecker = (*Module)(nil)

