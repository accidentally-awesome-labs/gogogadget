package testdb_test

import (
	"context"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/gogogadget/gogogadget/internal/db/testdb"
)

// A per-package database that is created and never dropped accumulates one row
// in pg_database per package, forever. At ~40 packages that is ~40 idle
// databases competing for a fixed max_connections, which is exactly what made
// unrelated integration tests fail intermittently under full parallel load.
//
// The database is dropped when the test that created it passes, and kept when
// it fails: a failed run is the one time the data is worth inspecting.
func TestPassingTestDropsItsDatabase(t *testing.T) {
	admin := adminConn(t)

	const name = "cleanupprobe"
	var passed bool
	t.Run("inner", func(t *testing.T) {
		testdb.DSN(t, name)
		if !databaseExists(t, admin, "gogogadget_test_"+name) {
			t.Fatal("DSN must create the database it returns")
		}
		passed = true
	})

	if !passed {
		t.Fatal("inner subtest did not run")
	}
	if databaseExists(t, admin, "gogogadget_test_"+name) {
		t.Fatal("a passing test must drop its database, or every package leaks one forever")
	}
}

func adminConn(t *testing.T) *pgx.Conn {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		base = "postgres://postgres:postgres@localhost:5432/gogogadget_test?sslmode=disable"
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("TEST_DATABASE_URL: %v", err)
	}
	u.Path = "/postgres"
	conn, err := pgx.Connect(context.Background(), u.String())
	if err != nil {
		t.Skipf("test database server unreachable: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func databaseExists(t *testing.T, conn *pgx.Conn, name string) bool {
	t.Helper()
	var count int
	if err := conn.QueryRow(context.Background(),
		`SELECT count(*) FROM pg_database WHERE datname = $1`, name).Scan(&count); err != nil {
		t.Fatalf("query pg_database: %v", err)
	}
	return count > 0
}
