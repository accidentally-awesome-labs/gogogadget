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
	// The suffix is part of the name, so the assertion has to read it from the
	// same place the code under test does. Hardcoding the bare name would make
	// this pass only when TEST_DB_SUFFIX is unset and fail for every concurrent
	// worker that sets one - reporting a leak that is not there.
	full := "gogogadget_test_" + name + os.Getenv("TEST_DB_SUFFIX")
	var passed bool
	t.Run("inner", func(t *testing.T) {
		testdb.DSN(t, name)
		if !databaseExists(t, admin, full) {
			t.Fatalf("DSN must create the database it returns (%s)", full)
		}
		passed = true
	})

	if !passed {
		t.Fatal("inner subtest did not run")
	}
	if databaseExists(t, admin, full) {
		t.Fatalf("a passing test must drop its database (%s), or every package leaks one forever", full)
	}
}

// Two concurrent workers on one server must not share a database name: the fixed
// per-package name made one worker's drop land inside another's create, failing
// with errors that named neither the test nor the cause.
func TestSuffixSeparatesConcurrentWorkers(t *testing.T) {
	admin := adminConn(t)
	const name = "suffixprobe"

	t.Setenv("TEST_DB_SUFFIX", "_workerone")
	one := testdb.DSN(t, name)
	t.Setenv("TEST_DB_SUFFIX", "_workertwo")
	two := testdb.DSN(t, name)

	if one == two {
		t.Fatalf("both workers were handed the same database: %s", one)
	}
	for _, want := range []string{"gogogadget_test_" + name + "_workerone", "gogogadget_test_" + name + "_workertwo"} {
		if !databaseExists(t, admin, want) {
			t.Fatalf("expected %s to exist", want)
		}
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
