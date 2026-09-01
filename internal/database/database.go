// Package database defines the opinionated Postgres capability contract.
package database

import (
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Pool = pgxpool.Pool
type Queries = sqlc.Queries
