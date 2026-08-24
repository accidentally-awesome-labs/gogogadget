package db

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/jackc/pgx/v5"
)

// moduleTestDSN returns a DSN for a scratch database, skipping when no server is
// reachable. The database module is the one place that opens and migrates, so
// this test needs a real server; CI provides one.
func moduleTestDSN(t *testing.T) string {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		base = "postgres://postgres:postgres@localhost:5432/gogogadget_test?sslmode=disable"
	}
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatalf("TEST_DATABASE_URL: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	admin := *parsed
	admin.Path = "/postgres"
	conn, err := pgx.Connect(ctx, admin.String())
	if err != nil {
		t.Skipf("test database server unreachable: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()

	name := "gogogadget_test_dbmodule"
	quoted := `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
	if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+quoted+" WITH (FORCE)"); err != nil {
		t.Fatalf("drop %s: %v", name, err)
	}
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}

	target := *parsed
	target.Path = "/" + name
	return target.String()
}

// The database module owns opening the pool and running migrations, and its stop
// hook owns closing the pool. All three move together: a runtime that boots a
// pool it cannot close leaks connections on every shutdown.
func TestNewModuleOpensMigratesAndStops(t *testing.T) {
	dsn := moduleTestDSN(t)
	host := apphost.Map(nil, time.Now(), "test")

	module, err := NewModule(context.Background(), host, Deps{
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
	_, err := NewModule(context.Background(), host, Deps{
		Config: &config.Config{
			Env:         "test",
			DatabaseURL: "postgres://nobody:nobody@127.0.0.1:1/absent?sslmode=disable&connect_timeout=1",
		},
	})
	if err == nil {
		t.Fatal("NewModule(unreachable) = nil error, want failure")
	}
}

func TestNewModuleRejectsMissingConfig(t *testing.T) {
	host := apphost.Map(nil, time.Now(), "test")
	if _, err := NewModule(context.Background(), host, Deps{}); err == nil {
		t.Fatal("NewModule(nil config) = nil error, want failure")
	}
}
