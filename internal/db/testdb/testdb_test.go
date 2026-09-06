package testdb_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/db/testdb"
)

// The fallback used to be a literal localhost:5432 — the DEVELOPMENT port,
// which agreed with the other three hardcoded copies only by luck and
// disagreed with the test stack outright. On a machine running its own
// Postgres there, `go test ./...` created, migrated and dropped databases on a
// server the project had nothing to do with.
//
// Mutation: return a literal instead of config.DerivedValue("test", …), and
// the integration layer stops following the port this project's test stack
// publishes.
func TestBaseDSNDerivesTheTestStackAndHonoursTheOverride(t *testing.T) {
	// A project whose test database is managed derives nothing, which BaseDSN
	// treats as legitimate and answers with a refusal naming
	// TEST_DATABASE_URL. Asserting the derived branch there would fail a
	// derivative for doing the supported thing, so it is skipped — the
	// override half below still runs.
	if derived, ok := config.DerivedValue("test", "DATABASE_URL"); !ok {
		t.Log("this project's test environment publishes no local Postgres; nothing to derive")
	} else {
		t.Setenv("TEST_DATABASE_URL", "")
		if got := testdb.BaseDSN(t); got != derived {
			t.Fatalf("BaseDSN = %q, want the derived test address %q", got, derived)
		}
	}

	// CI points the suite at its own service container this way, so the
	// override has to outrank the derivation.
	const override = "postgres://postgres:postgres@localhost:5432/gogogadget_test?sslmode=disable"
	t.Setenv("TEST_DATABASE_URL", override)
	if got := testdb.BaseDSN(t); got != override {
		t.Fatalf("BaseDSN = %q, want the exported override %q", got, override)
	}
}

// The absent/broken distinction, and the one this package used to get wrong
// in the direction that reports a pass. The test stack was torn down between
// rounds, every fixture in `internal/audit`, `internal/notify`,
// `internal/schedules` and `internal/usage` skipped, `go test` printed `ok`
// for all four, and "targeted tests pass" was reported on that basis.
//
// "Is it set" and "is it defaulted" are different questions and only the
// first means somebody asked for a database, so the predicate is the
// environment variable and not the resolved string — a derived address is
// present whether or not anything answers on it.
//
// Mutation: skip unconditionally, or test the resolved DSN for emptiness
// instead of the variable, and a named server that is down reports a pass
// again.
func TestUnreachableFailsANamedServerAndSkipsADerivedOne(t *testing.T) {
	down := errors.New("connect: connection refused")

	t.Run("named server", func(t *testing.T) {
		t.Setenv("TEST_DATABASE_URL", "postgres://postgres:postgres@localhost:15432/gogogadget?sslmode=disable")
		spy := &verdictSpy{}
		testdb.Unreachable(spy, down)
		if spy.skipped {
			t.Fatal("an explicitly named server that does not answer was reported as an absence")
		}
		if !spy.failed {
			t.Fatal("an explicitly named server that does not answer neither failed nor skipped")
		}
		if !strings.Contains(spy.message, "TEST_DATABASE_URL") {
			t.Fatalf("failure message %q does not name the variable that made this a failure", spy.message)
		}
	})

	t.Run("derived server", func(t *testing.T) {
		t.Setenv("TEST_DATABASE_URL", "")
		spy := &verdictSpy{}
		testdb.Unreachable(spy, down)
		if spy.failed {
			t.Fatal("a contributor who never asked for a database was failed instead of skipped")
		}
		if !spy.skipped {
			t.Fatal("a derived address that does not answer neither failed nor skipped")
		}
		if !strings.Contains(spy.message, "services up") {
			t.Fatalf("skip message %q does not name the command that would run the layer", spy.message)
		}
	})
}

// verdictSpy records which of the two terminal calls Unreachable made. A real
// *testing.T cannot answer that question from the test that provoked it: both
// calls end the goroutine, and neither status is readable from the outside.
type verdictSpy struct {
	failed  bool
	skipped bool
	message string
}

func (s *verdictSpy) Helper() {}
func (s *verdictSpy) Fatalf(format string, args ...any) {
	s.failed = true
	s.message = fmt.Sprintf(format, args...)
}
func (s *verdictSpy) Skipf(format string, args ...any) {
	s.skipped = true
	s.message = fmt.Sprintf(format, args...)
}

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
	u, err := url.Parse(testdb.BaseDSN(t))
	if err != nil {
		t.Fatalf("test database server: %v", err)
	}
	u.Path = "/postgres"
	conn, err := pgx.Connect(context.Background(), u.String())
	if err != nil {
		testdb.Unreachable(t, err)
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
