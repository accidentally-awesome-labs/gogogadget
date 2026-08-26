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

// Phase budgets. One shared deadline for the whole of Open was wrong twice
// over: the retry backoff below spent the same clock the migrations needed,
// and the migration set grows with the schema, so a fixed budget silently
// tightened every time a migration was added. Each phase now gets a budget
// sized to its own job.
const (
	// connectTimeout is a reachability probe. No server means Skip, so this
	// only needs to be long enough to distinguish "absent" from "busy".
	connectTimeout = 10 * time.Second
	// ddlTimeout covers ONE drop or create attempt, so the backoff between
	// attempts cannot starve anything downstream.
	ddlTimeout = 15 * time.Second
	// migrateTimeout covers opening the pool and running every embedded
	// migration. This is the phase that grows, and on a server shared with
	// other work it is the phase that stalls, so it is sized for a loaded
	// machine rather than for today's migration count.
	migrateTimeout = 2 * time.Minute
)

// Open drops, recreates, and migrates gogogadget_test_<name> on the server
// from TEST_DATABASE_URL (default postgres://postgres:postgres@localhost:5432).
func Open(t *testing.T, name string) (*pgxpool.Pool, *sqlc.Queries) {
	t.Helper()
	dsn := DSN(t, name)

	migrateCtx, cancelMigrate := context.WithTimeout(context.Background(), migrateTimeout)
	defer cancelMigrate()

	pool, err := db.Open(migrateCtx, dsn)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	if err := db.Migrate(migrateCtx, pool); err != nil {
		pool.Close()
		t.Fatalf("migrate %s: %v", name, err)
	}
	t.Cleanup(pool.Close)
	return pool, sqlc.New(pool)
}

// DSN drops and recreates gogogadget_test_<name> and returns its DSN, without
// migrating. Callers that own their own migration run — a runtime booting the
// database module, for instance — need an empty database, not a prepared one.
//
// TEST_DB_SUFFIX is appended to the database name. The name is otherwise fixed
// per package, and Open drops the database before recreating it, so two
// processes running the same package against one server interleave a drop with a
// create and fail with errors that name neither the test nor the cause
// ("duplicate key value violates unique constraint pg_database_datname_index",
// or a connection terminated by administrator command). Those look like real
// failures in whatever package happened to be running. The suffix gives each
// concurrent worker its own database on the same server; it defaults to empty so
// a single run keeps the stable name, and reusing one suffix per worker means
// databases are recycled rather than accumulating.
func DSN(t *testing.T, name string) string {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		base = "postgres://postgres:postgres@localhost:5432/gogogadget_test?sslmode=disable"
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("TEST_DATABASE_URL: %v", err)
	}
	dbName := "gogogadget_test_" + name + os.Getenv("TEST_DB_SUFFIX")

	connectCtx, cancelConnect := context.WithTimeout(context.Background(), connectTimeout)
	defer cancelConnect()

	admin := *u
	admin.Path = "/postgres"
	conn, err := pgx.Connect(connectCtx, admin.String())
	if err != nil {
		t.Skipf("test database server unreachable: %v", err)
	}
	// Closing gets its own context: the phase budget that opened the
	// connection is typically spent by the time this runs.
	defer func() { _ = conn.Close(context.Background()) }()

	q := `"` + strings.ReplaceAll(dbName, `"`, `""`) + `"`
	// DROP races the previous test's pool teardown: pool.Close() closes
	// sockets client-side, and the server backend can lag a few ms behind.
	// Retrying is the boring fix, and it also covers a drop that timed out
	// against a momentarily busy server; a real permission failure fails
	// all attempts and still surfaces.
	var dropErr error
	for attempt := range 5 {
		// WITH (FORCE) terminates every remaining backend, and it fails
		// OUTRIGHT - "must be a member of the role whose process is being
		// terminated" - if even one of them belongs to another role. Under a
		// full parallel run that is enough to fail a package with an error
		// naming no test and no cause, which is exactly what it did.
		//
		// The sessions that actually need to go are our own pools', so they are
		// terminated first and by name. Then a plain DROP, which succeeds once
		// nothing is connected; FORCE is the last resort rather than the first
		// move.
		terminateOwnBackends(conn, dbName)
		if dropErr = execDDL(conn, `DROP DATABASE IF EXISTS `+q); dropErr == nil {
			break
		}
		if dropErr = execDDL(conn, `DROP DATABASE IF EXISTS `+q+` WITH (FORCE)`); dropErr == nil {
			break
		}
		if attempt == 4 {
			t.Fatalf("drop %s: %v", dbName, dropErr)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := execDDL(conn, `CREATE DATABASE `+q); err != nil {
		t.Fatalf("create %s: %v", dbName, err)
	}

	// Drop the database again when the test finishes, but only if it passed.
	// Without this every package that ever ran leaves one database behind
	// forever, and a few dozen idle databases competing for a fixed
	// max_connections is what makes unrelated integration tests fail
	// intermittently under full parallel load.
	//
	// A failed test keeps its database on purpose: that is the one occasion the
	// rows are worth inspecting, and the next run drops it before recreating.
	t.Cleanup(func() {
		if t.Failed() {
			return
		}
		dropCtx, cancelDrop := context.WithTimeout(context.Background(), connectTimeout)
		defer cancelDrop()
		cleanupConn, err := pgx.Connect(dropCtx, admin.String())
		if err != nil {
			return
		}
		defer func() { _ = cleanupConn.Close(context.Background()) }()
		terminateOwnBackends(cleanupConn, dbName)
		if execDDL(cleanupConn, `DROP DATABASE IF EXISTS `+q) != nil {
			_ = execDDL(cleanupConn, `DROP DATABASE IF EXISTS `+q+` WITH (FORCE)`)
		}
	})

	u.Path = "/" + dbName
	return u.String()
}

// terminateOwnBackends closes the sessions this role opened against dbName.
// Best effort by design: a session that has already gone, or one we may not
// touch, is not a reason to stop - the drop that follows reports whatever is
// genuinely in the way.
func terminateOwnBackends(conn *pgx.Conn, dbName string) {
	ctx, cancel := context.WithTimeout(context.Background(), ddlTimeout)
	defer cancel()
	_, _ = conn.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE datname = $1 AND pid <> pg_backend_pid() AND usename = current_user`, dbName)
}

// execDDL runs one CREATE/DROP DATABASE statement under its own budget.
func execDDL(conn *pgx.Conn, sql string) error {
	ctx, cancel := context.WithTimeout(context.Background(), ddlTimeout)
	defer cancel()
	_, err := conn.Exec(ctx, sql)
	return err
}
