// Package testdb gives every integration-test package its OWN database:
// `go test ./...` runs packages in parallel, and sharing one database lets
// one package's teardown nuke another's fixtures. Databases are named
// gogogadget_test_<name>, dropped and recreated at Open so every run starts
// clean. Skips when the server is unreachable (CI provides it).
package testdb

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/db"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Open drops, recreates, and migrates gogogadget_test_<name> on the server
// from TEST_DATABASE_URL (default postgres://postgres:postgres@localhost:5432).
func Open(t *testing.T, name string) (*pgxpool.Pool, *sqlc.Queries) {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		base = "postgres://postgres:postgres@localhost:5432/gogogadget_test?sslmode=disable"
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("TEST_DATABASE_URL: %v", err)
	}
	dbName := "gogogadget_test_" + name

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	admin := *u
	admin.Path = "/postgres"
	conn, err := pgx.Connect(ctx, admin.String())
	if err != nil {
		t.Skipf("test database server unreachable: %v", err)
	}
	defer conn.Close(ctx)

	q := `"` + strings.ReplaceAll(dbName, `"`, `""`) + `"`
	if _, err := conn.Exec(ctx, `DROP DATABASE IF EXISTS `+q+` WITH (FORCE)`); err != nil {
		t.Fatalf("drop %s: %v", dbName, err)
	}
	if _, err := conn.Exec(ctx, `CREATE DATABASE `+q); err != nil {
		t.Fatalf("create %s: %v", dbName, err)
	}

	u.Path = "/" + dbName
	pool, err := db.Open(ctx, u.String())
	if err != nil {
		t.Fatalf("open %s: %v", dbName, err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("migrate %s: %v", dbName, err)
	}
	t.Cleanup(pool.Close)
	return pool, sqlc.New(pool)
}
