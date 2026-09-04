// cmd/seed loads fixtures into the database named by DATABASE_URL. With -reset
// it first drops and recreates that database, then applies embedded migrations.
//
// The default is -registry <dev|e2e>: the generated fragment order from
// internal/db (module order), so a module's fixture loads with the module. A
// positional .sql path is still accepted for one-off fixtures.
package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	contentfs "github.com/gogogadget/gogogadget/content"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/db"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
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
	registrySet := ""
	explicit := []string{}
	for i := 0; i < len(args); i++ {
		if args[i] == "-registry" && i+1 < len(args) {
			registrySet = args[i+1]
			i++
			continue
		}
		explicit = append(explicit, args[i])
	}
	if registrySet != "" && len(explicit) > 0 {
		return fmt.Errorf("usage: seed [-reset] [-registry dev|e2e] [file.sql]")
	}
	files := explicit
	if registrySet != "" {
		fragments, ok := db.SeedFragments[registrySet]
		if !ok || len(fragments) == 0 {
			return fmt.Errorf("no seed fragments registered for set %q", registrySet)
		}
		files = fragments
	}
	if len(files) == 0 {
		return fmt.Errorf("usage: seed [-reset] [-registry dev|e2e] [file.sql]")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := refuseUnnamedDatabase(cfg); err != nil {
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

	for _, seedFile := range files {
		sql, err := os.ReadFile(seedFile)
		if err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("seed %s: %w", seedFile, err)
		}
	}
	fmt.Printf("seeded %d fragment(s)\n", len(files))

	// The shipped markdown is the content corpus: import it into
	// content_entries so a fresh clone has a populated blog and changelog.
	// Idempotent by (kind, slug) — re-seeding never clobbers an edited row.
	posts, releases, err := content.Import(ctx, sqlc.New(pool), contentfs.FS)
	if err != nil {
		return fmt.Errorf("import content: %w", err)
	}
	fmt.Printf("imported %d posts, %d releases\n", posts, releases)
	return nil
}

// refuseUnnamedDatabase refuses to mutate a database nobody named.
//
// Seeding migrates and writes, and -reset drops the database outright. None of
// that may ride on DATABASE_URL's declared default, which is a documented
// guess at a live local address
// (postgres://postgres:postgres@localhost:5432/gogogadget) rather than
// something this project chose. Run outside the CLI in a project with nothing
// configured, this program resolved that default, migrated and seeded whatever
// answered there, and exited 0 — on a release host, the operator's own
// Postgres.
//
// A derived value is accepted. It reflects the database adapter this project
// selected and the host port this environment publishes, so it names the
// project's own server; the zero-account path depends on that, which is why
// the answer is provenance rather than removing the default.
func refuseUnnamedDatabase(cfg config.Config) error {
	if cfg.Source("DATABASE_URL") != config.SourceDeclaredDefault {
		return nil
	}
	return fmt.Errorf(
		"refusing to migrate and seed a database nobody named: DATABASE_URL is only supplied by its declared default (%s) for the %s environment; "+
			"export it, or run `ggg db seed`, which resolves the address this project's own selection publishes",
		cfg.DatabaseURL, cfg.Env)
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
