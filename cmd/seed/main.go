// cmd/seed loads a SQL fixture into the database named by DATABASE_URL.
// With -reset it first drops and recreates that database, then applies
// embedded migrations. Usage: go run ./cmd/seed [-reset] path/to/seed.sql
package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/db"
	"github.com/jackc/pgx/v5"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	reset := false
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "-reset" {
		reset = true
		args = args[1:]
	}
	if len(args) != 1 {
		return fmt.Errorf("usage: seed [-reset] <file.sql>")
	}
	seedFile := args[0]

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()

	if reset {
		if err := resetDatabase(ctx, cfg.DatabaseURL); err != nil {
			return fmt.Errorf("reset: %w", err)
		}
	}

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	sql, err := os.ReadFile(seedFile)
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, string(sql)); err != nil {
		return fmt.Errorf("seed %s: %w", seedFile, err)
	}
	fmt.Println("seeded", seedFile)
	return nil
}

// resetDatabase drops and recreates the database named in rawURL by connecting
// to the server's maintenance database.
func resetDatabase(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	name := strings.TrimPrefix(u.Path, "/")
	if name == "" || name == "postgres" {
		return fmt.Errorf("refusing to reset database %q", name)
	}

	maintenance := *u
	maintenance.Path = "/postgres"
	conn, err := pgx.Connect(ctx, maintenance.String())
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	// Identifiers can't be parameterized; quote defensively.
	q := `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
	if _, err := conn.Exec(ctx, `DROP DATABASE IF EXISTS `+q+` WITH (FORCE)`); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, `CREATE DATABASE `+q); err != nil {
		return err
	}
	fmt.Println("reset database", name)
	return nil
}
