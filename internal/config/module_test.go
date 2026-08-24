package config

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
)

// The config module is the root of the dependency graph: it parses through the
// host, so a runtime can boot against a fixed environment map instead of the
// process environment.
func TestNewModuleReadsHostEnvironment(t *testing.T) {
	h := apphost.Map(map[string]string{
		"APP_ENV":      "test",
		"APP_URL":      "http://localhost:8080",
		"DATABASE_URL": "postgres://localhost/test",
	}, time.Now(), "v-test")

	m, err := NewModule(context.Background(), h, Deps{})
	if err != nil {
		t.Fatalf("NewModule: %v", err)
	}
	if m.Config == nil {
		t.Fatal("Config = nil, want a parsed config")
	}
	if got := m.Config.Env; got != "test" {
		t.Fatalf("Env = %q, want %q", got, "test")
	}
	if got := m.Config.DatabaseURL; got != "postgres://localhost/test" {
		t.Fatalf("DatabaseURL = %q, want the host value", got)
	}
}

// Outside production a missing DSN falls back to the local dev database — that
// fallback is what makes a fresh clone run with zero configuration.
func TestNewModuleAppliesDevDatabaseFallback(t *testing.T) {
	h := apphost.Map(map[string]string{"APP_ENV": "test"}, time.Now(), "v-test")

	m, err := NewModule(context.Background(), h, Deps{})
	if err != nil {
		t.Fatalf("NewModule: %v", err)
	}
	if m.Config.DatabaseURL == "" {
		t.Fatal("DatabaseURL = empty, want the local dev fallback DSN")
	}
}

// Production refuses the dev bypass and the fallback DSN: booting production
// against the wrong database, or trusting synthetic tokens, must not be
// reachable by omission.
func TestNewModuleRefusesUnsafeProduction(t *testing.T) {
	h := apphost.Map(map[string]string{
		"APP_ENV":         "production",
		"APP_URL":         "https://example.com",
		"DEV_AUTH_BYPASS": "true",
	}, time.Now(), "v-test")

	_, err := NewModule(context.Background(), h, Deps{})
	if err == nil {
		t.Fatal("NewModule(production bypass) = nil error, want refusal")
	}
	for _, want := range []string{"DEV_AUTH_BYPASS", "DATABASE_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
}

// The host environment is the only source: a value set in the process must not
// leak into a module booted against a fixed map.
func TestNewModuleIgnoresProcessEnvironment(t *testing.T) {
	t.Setenv("APP_URL", "https://leaked.example.com")

	h := apphost.Map(map[string]string{"APP_ENV": "test"}, time.Now(), "v-test")
	m, err := NewModule(context.Background(), h, Deps{})
	if err != nil {
		t.Fatalf("NewModule: %v", err)
	}
	if m.Config.AppURL == "https://leaked.example.com" {
		t.Fatal("AppURL came from the process environment, want the host map")
	}
}
