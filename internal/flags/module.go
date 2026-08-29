// Package-level module wiring. Deps/NewModule is the uniform constructor shape
// the generated bootstrap calls.
package flags

import (
	"context"
	"fmt"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
)

// cacheTTL is how stale a flag decision may be. It is fixed by the module rather
// than configured so every deployment agrees on the bound; admin mutations call
// Invalidate, so a change is visible immediately rather than after the TTL.
const cacheTTL = 30 * time.Second

// Deps is the typed dependency set the generated bootstrap supplies.
type Deps struct {
	Queries *sqlc.Queries
}

// Module is the constructed feature-flag closure.
type Module struct {
	Evaluator Evaluator
	// DB is the concrete evaluator, exposed so admin mutations can invalidate
	// the cache. The narrow Evaluator port stays the thing request paths take.
	DB *DBEvaluator
}

// NewModule builds the database-backed evaluator. Flags are read on request
// paths, so a missing query handle is a wiring bug that must fail at boot rather
// than nil-panic under traffic.
func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if d.Queries == nil {
		return nil, fmt.Errorf("feature flags: queries dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_ = h
	evaluator := NewDBEvaluator(d.Queries, cacheTTL)
	return &Module{Evaluator: evaluator, DB: evaluator}, nil
}
