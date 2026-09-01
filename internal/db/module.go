// Package-level module wiring. Deps/NewModule is the uniform constructor shape
// the generated bootstrap calls. The database module owns the pool, the
// migration run, and the pool's shutdown: a runtime that opens a pool it cannot
// close leaks connections on every restart.
package db

import (
	"context"
	"fmt"
	"sync"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/telemetry"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Deps is the typed dependency set the generated bootstrap supplies.
type Deps struct {
	Config    *config.Config
	Telemetry telemetry.Providers
}

// Module is the constructed database closure.
type Module struct {
	Pool    *pgxpool.Pool
	Queries *sqlc.Queries

	closeOnce sync.Once
	closed    chan struct{}
}

// NewModule opens the pool, verifies it, and runs every embedded migration. An
// unreachable or unmigratable database is a boot failure: every request path
// needs it, so serving traffic without it would only produce errors.
func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if d.Config == nil {
		return nil, fmt.Errorf("database: config dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	pool, err := Open(ctx, d.Config.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := telemetry.PGX(ctx, d.Telemetry, "migrate", func(ctx context.Context) error { return Migrate(ctx, pool) }); err != nil {
		// The pool is ours and nothing else can reach it yet, so close it here
		// rather than leaking it on a failed boot.
		pool.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	h.Log().Info("database: ready")
	return &Module{
		Pool:    pool,
		Queries: sqlc.New(pool),
		closed:  make(chan struct{}),
	}, nil
}

// Stop closes the pool. It is idempotent because shutdown paths can reach it
// more than once, and it honors ctx because pgxpool.Close blocks until every
// connection is released — one stuck query must not block shutdown forever.
func (m *Module) Stop(ctx context.Context) error {
	if m == nil || m.Pool == nil {
		return nil
	}
	m.closeOnce.Do(func() {
		go func() {
			m.Pool.Close()
			close(m.closed)
		}()
	})
	select {
	case <-m.closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
