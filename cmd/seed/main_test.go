package main

import (
	"os"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/config"
)

// cmd/seed migrates and seeds, and with -reset it drops the database outright.
// It bypasses the CLI by design — `ggg db seed` invokes it, and so does the
// visual harness — so the refusal `ggg db` applies to a default-sourced DSN
// has to hold here too.
//
// The failure this pins: with nothing configured, config.Load resolved
// DATABASE_URL's declared default (localhost:5432) and this program migrated
// and seeded whatever answered there, exiting 0. On a host running its own
// Postgres that is not the project's database at all.
//
// Mutation: refuse on any source but SourceDeclaredDefault — invert the
// comparison, or drop the check — and either a fresh project can no longer
// seed the database it named, or it can again seed one nobody named.
func TestSeedRefusesOnlyADefaultSourcedDatabaseURL(t *testing.T) {
	const address = "postgres://postgres:postgres@localhost:5432/gogogadget?sslmode=disable"
	for _, testCase := range []struct {
		name   string
		source config.ValueSource
		refuse bool
	}{
		// The operator, or the deployment, said so.
		{"environment", config.SourceEnvironment, false},
		// This project's own selection and published ports say so. The
		// zero-account path depends on this staying allowed.
		{"derived", config.SourceDerived, false},
		// Nobody said so. This is the one that mutated the wrong server.
		{"declared default", config.SourceDeclaredDefault, true},
		// Nothing resolved it at all; the connection attempt fails on its own
		// terms and says so.
		{"unset", config.SourceUnset, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := config.Config{
				Env:         "development",
				DatabaseURL: address,
				Sources:     map[string]config.ValueSource{"DATABASE_URL": testCase.source},
			}
			err := refuseUnnamedDatabase(cfg)
			if testCase.refuse {
				if err == nil {
					t.Fatalf("a %s value was accepted for a migrating, seeding run", testCase.source)
				}
				// The refusal has to be actionable: which key, which value,
				// which environment, and what to run instead.
				for _, want := range []string{"refusing", "DATABASE_URL", "declared default", address, "development", "ggg db seed"} {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("refusal %q does not name %q", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("a %s value was refused: %v", testCase.source, err)
			}
		})
	}
}

// The refusal must be reachable from run(), not just as a function nobody
// calls. Production is the environment where nothing derives, so the declared
// default is what run() would resolve — and config.Load refuses production
// without an explicit DATABASE_URL first, which is the same refusal one layer
// up. Either way run() must not reach a database.
//
// Mutation: delete the refuseUnnamedDatabase call at main.go, and a project
// where nothing derives migrates and seeds the declared default's address.
func TestSeedRunRefusesBeforeTouchingADatabase(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, key := range config.ConfigRegistry {
		t.Setenv(key, "")
	}
	t.Setenv("APP_ENV", "production")

	original := os.Args
	t.Cleanup(func() { os.Args = original })
	os.Args = []string{"seed", "-registry", "dev"}

	err := run()
	if err == nil {
		t.Fatal("run() proceeded with no database anyone named")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("refusal %q does not name DATABASE_URL", err)
	}
}

// The refusal must be unreachable on the documented local paths, or it would
// block the zero-account posture instead of the hazard. Both environments that
// publish a local Postgres derive their address, so neither ever reaches the
// declared default.
//
// Mutation: stop emitting derivedValues for an environment, and that
// environment falls through to localhost:5432 and is refused — which is how
// `ggg db migrate --environment test` behaved before this existed.
//
// An environment that derives nothing is legitimate — a project may select a
// managed database — so this skips rather than fails there. The shipped code
// treats that state as valid, and a test that contradicts its own contract
// would fail in a derivative for doing the supported thing.
func TestSeedableEnvironmentsDeriveTheirDatabase(t *testing.T) {
	for _, environment := range []string{"development", "test"} {
		address, ok := config.DerivedValue(environment, "DATABASE_URL")
		if !ok {
			t.Logf("%s publishes no local database; nothing to derive", environment)
			continue
		}
		cfg := config.Config{
			Env:         environment,
			DatabaseURL: address,
			Sources:     map[string]config.ValueSource{"DATABASE_URL": config.SourceDerived},
		}
		if err := refuseUnnamedDatabase(cfg); err != nil {
			t.Fatalf("%s cannot seed its own derived database: %v", environment, err)
		}
	}
}
