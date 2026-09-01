package session

import (
    "context"
    "fmt"

    "github.com/gogogadget/gogogadget/internal/apphost"
    "github.com/gogogadget/gogogadget/internal/config"
    "github.com/gogogadget/gogogadget/internal/identity"
    "github.com/jackc/pgx/v5/pgxpool"
)

type Deps struct {
    Config *config.Config
    Pool *pgxpool.Pool
    Verify identity.Verifier
    Fetch identity.UserFetcher
}

type Module struct { Loader Loader }

func NewModule(ctx context.Context, _ apphost.Host, d Deps) (*Module, error) {
    if d.Config == nil || d.Pool == nil || d.Verify == nil || d.Fetch == nil { return nil, fmt.Errorf("identity session: dependencies are required") }
    if err := ctx.Err(); err != nil { return nil, err }
    return &Module{Loader: &SessionLoader{Pool: d.Pool, Verify: d.Verify, Fetch: d.Fetch}}, nil
}
