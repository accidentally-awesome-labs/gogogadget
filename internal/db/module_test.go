package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/db"
	"github.com/gogogadget/gogogadget/internal/db/testdb"
)

// moduleTestDSN returns a DSN for a scratch database, skipping when no server
// is reachable. It delegates to testdb rather than running its own CREATE and
// DROP: this test lives in the external test package precisely so it can, and
// the shared helper is also what drops the database afterwards instead of
// leaving one behind on every run.
func moduleTestDSN(t *testing.T) string {
	t.Helper()
	return testdb.DSN(t, "dbmodule")
}

// The database module owns opening the pool and running migrations, and its stop
// hook owns closing the pool. All three move together: a runtime that boots a
// pool it cannot close leaks connections on every shutdown.
func TestNewModuleOpensMigratesAndStops(t *testing.T) {
	dsn := moduleTestDSN(t)
	host := apphost.Map(nil, time.Now(), "test")

	module, err := db.NewModule(context.Background(), host, db.Deps{
		Config: &config.Config{Env: "test", DatabaseURL: dsn},
	})
	if err != nil {
		t.Fatalf("NewModule: %v", err)
	}
	if module.Pool == nil {
		t.Fatal("Pool = nil")
	}
	if module.Queries == nil {
		t.Fatal("Queries = nil")
	}

	// Migrations ran: a table the first migration creates must exist.
	var exists bool
	if err := module.Pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		 WHERE table_schema = 'public' AND table_name = 'users')`).Scan(&exists); err != nil {
		t.Fatalf("probe schema: %v", err)
	}
	if !exists {
		t.Fatal("NewModule did not run migrations: users table is absent")
	}

	if err := module.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Stop must be idempotent: shutdown paths can call it more than once.
	if err := module.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if _, err := module.Pool.Exec(context.Background(), "SELECT 1"); err == nil {
		t.Fatal("pool still usable after Stop")
	}
}

// An unreachable database is a boot failure, not a degraded mode: every request
// path needs it, so serving traffic without it would only produce errors.
func TestNewModuleFailsWhenDatabaseUnreachable(t *testing.T) {
	host := apphost.Map(nil, time.Now(), "test")
	_, err := db.NewModule(context.Background(), host, db.Deps{
		Config: &config.Config{
			Env:         "test",
			DatabaseURL: "postgres://nobody:nobody@127.0.0.1:1/absent?sslmode=disable&connect_timeout=1",
		},
	})
	if err == nil {
		t.Fatal("db.NewModule(unreachable) = nil error, want failure")
	}
}

func TestNewModuleRejectsMissingConfig(t *testing.T) {
	host := apphost.Map(nil, time.Now(), "test")
	if _, err := db.NewModule(context.Background(), host, db.Deps{}); err == nil {
		t.Fatal("db.NewModule(nil config) = nil error, want failure")
	}
}
