// Package config parses and validates environment configuration.
// Stdlib only — no env library. `.env` is auto-loaded in development only,
// via a tiny inline KEY=VALUE parser.
//
// The Config struct and every per-key parse are generated from module env
// declarations (config_registry_gen.go), so a setting and the field it lands in
// have one owner. What stays here is behaviour over those values: cross-field
// derivations, the environment predicates, the render clock, and the readers.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LoadFrom reads configuration through lookup and validates the result. All
// validation problems are reported together — an operator fixing one bad value
// should not have to re-run to discover the next.
//
// Modules parse through this so a runtime can boot against a fixed environment
// without touching the process.
func LoadFrom(lookup func(string) string) (Config, error) {
	cfg, errs := parseDeclared(lookup)
	// Provider adapters own these keys, but the shared Config carries their
	// values so selected adapters can consume one parsed snapshot. Keep this
	// lookup isolated from generated fields; unselected adapters remain absent
	// from the typed struct while Value stays available to their constructors.
	for _, key := range []string{"POSTHOG_API_KEY", "POSTHOG_HOST", "SENTRY_DSN", "LLM_API_KEY", "LLM_BASE_URL", "LLM_MODEL"} {
		cfg.Values[key] = lookup(key)
	}

	// Cross-field behaviour, which is not expressible as a per-key declaration.

	// Chattier by default while developing; quiet enough to be readable in a log
	// aggregator otherwise.
	if cfg.LogLevel == "" {
		if cfg.Development() {
			cfg.LogLevel = "debug"
		} else {
			cfg.LogLevel = "info"
		}
	}

	if cfg.Production() {
		errs = append(errs, requireProductionKeys(lookup)...)
	}

	return cfg, errors.Join(errs...)
}

// Load reads the process environment, auto-loading the CLI-managed
// .ggg/env/<environment>.env and, in development, the legacy .env.
//
// Precedence is the plan's: the process environment wins, then the
// CLI-managed file, then the legacy .env in development only, and no file at
// all in production. loadDotEnv never overwrites a key that is already set,
// so loading in this order is what implements it.
//
// The CLI-managed layer is the one that was missing. It is where `ggg
// provider configure` and genesis write development values, and where the
// generated compose.yaml points the app service's env_file — so a program run
// on the host rather than in the container saw none of it and fell back to
// DATABASE_URL's declared default, localhost:5432. `ggg db seed` reached the
// operator's own Postgres that way, migrated it, seeded it, and reported
// success.
func Load() (Config, error) {
	environment := pick(os.Getenv, "APP_ENV", "development")
	if environment != "production" {
		loadDotEnv(filepath.Join(".ggg", "env", environment+".env"))
		if environment == "development" {
			loadDotEnv(".env") // missing file is fine; real env wins
		}
	}
	return LoadFrom(os.Getenv)
}

func (c Config) Production() bool  { return c.Env == "production" }
func (c Config) Development() bool { return c.Env == "development" }

// Value exposes adapter-owned configuration after generated defaults and
// normalization have been applied. It deliberately does not expose fields for
// unselected provider candidates.
func (c Config) Value(key string) string {
	if c.Values == nil {
		return ""
	}
	return c.Values[key]
}

// ValueSource records where one resolved value came from. The distinction is
// load-bearing for anything that MUTATES: DATABASE_URL's declared default is
// postgres://postgres:postgres@localhost:5432/gogogadget, which is a live
// address on any machine that has ever run Postgres locally, so a program
// about to migrate and seed must be able to tell "this project publishes that
// address" from "nobody told me anything and this is the documented guess".
type ValueSource int

const (
	// SourceUnset: nothing supplied it and no default is declared.
	SourceUnset ValueSource = iota
	// SourceEnvironment: the lookup supplied it. Load merges the CLI-managed
	// .ggg/env/<environment>.env and, in development, the legacy .env into the
	// process environment first, so this is one layer here: the operator, or
	// the deployment, said so.
	SourceEnvironment
	// SourceDerived: this project's own provider selection and published host
	// ports resolve to it. Nobody typed it, but the project named it — which
	// is exactly the property a declared default lacks — so destructive work
	// may ride on it.
	SourceDerived
	// SourceDeclaredDefault: nobody supplied it, nothing derives it, and the
	// owning manifest declares a fallback. Fine to read; refused by anything
	// that mutates a database.
	SourceDeclaredDefault
)

func (s ValueSource) String() string {
	switch s {
	case SourceEnvironment:
		return "environment"
	case SourceDerived:
		return "derived"
	case SourceDeclaredDefault:
		return "declared default"
	default:
		return "unset"
	}
}

// resolve reads one declared key through the documented precedence and records
// where the value came from. The lookup wins (Load has already layered the
// files into it), then what this project derives for this environment, then
// the owning manifest's declared default.
//
// Empty and unset are the same thing at every layer, matching pick: an
// exported-but-blank variable is not a configuration choice, and treating it
// as one only means falling through to a default connection string — which is
// how a program reaches the wrong server.
func (c *Config) resolve(lookup func(string) string, environment, key, def string) string {
	if value := lookup(key); value != "" {
		c.Sources[key] = SourceEnvironment
		return value
	}
	if value := derivedValues[environment][key]; value != "" {
		c.Sources[key] = SourceDerived
		return value
	}
	if def != "" {
		c.Sources[key] = SourceDeclaredDefault
		return def
	}
	c.Sources[key] = SourceUnset
	return ""
}

// Source reports where the parsed value for key came from. A Config assembled
// by hand in a unit test reports SourceUnset for everything, which is honest:
// nothing resolved it.
func (c Config) Source(key string) ValueSource {
	if c.Sources == nil {
		return SourceUnset
	}
	return c.Sources[key]
}

// DerivedValue is what this project's provider selection and published host
// ports resolve to for one key in one environment, regardless of which
// environment this process runs in. Test harnesses need exactly that: an
// integration package's database lives on the test stack whatever APP_ENV
// says. Absent means the environment publishes nothing local to address.
func DerivedValue(environment, key string) (string, bool) {
	value, ok := derivedValues[environment][key]
	return value, ok
}

// IntValue returns a generated integer declaration without making adapters
// parse their own environment values. Config.LoadFrom validates declared
// bounds; this fallback only supports manually assembled Config values in
// adapter unit tests.
func (c Config) IntValue(key string) (int, error) {
	raw := c.Value(key)
	if raw == "" {
		return 0, fmt.Errorf("%s is not configured", key)
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s is invalid: %w", key, err)
	}
	return value, nil
}

// BoolValue reads a generated boolean declaration by key. It is the read a
// module that does not own the key must use: the typed field belongs to the
// declaring module and disappears with it, while the key either resolves or
// reads false.
func (c Config) BoolValue(key string) bool { return parseBool(c.Value(key)) }
func (c Config) Test() bool                { return c.Env == "test" }
func (c Config) PostHogEnabled() bool      { return c.Value("POSTHOG_API_KEY") != "" }
func (c Config) SentryEnabled() bool       { return c.Value("SENTRY_DSN") != "" }

// LLMConfigured reports whether an OpenAI-compatible backend is set. Empty →
// the AI route renders a 503 not-configured (same degrade as billing).
func (c Config) LLMConfigured() bool {
	return c.Value("LLM_API_KEY") != "" && c.Value("LLM_MODEL") != ""
}

// Now returns the render clock: frozen at TEST_NOW under APP_ENV=test,
// wall-clock otherwise. All rendered dates/times derive from this so visual
// baselines never rot.
//
// The test-environment gate lives here rather than in the parse: whether a
// parsed instant is honoured is behaviour, and a frozen clock leaking into
// development would be a confusing way to find that out.
func (c Config) Now() time.Time {
	if c.Test() && !c.testNow.IsZero() {
		return c.testNow
	}
	return time.Now()
}

// pick returns the looked-up value, or def when it is unset or empty. Empty
// and unset are the same thing here: an exported-but-blank variable is not a
// configuration choice.
func pick(lookup func(string) string, key, def string) string {
	if v := lookup(key); v != "" {
		return v
	}
	return def
}

func parseBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// loadDotEnv fills os environ from a KEY=VALUE file without overriding keys
// that already carry a value. Supports comments, blank lines, optional
// `export `, and single/double quoted values. Intentionally minimal.
//
// A key that is SET BUT EMPTY counts as absent, so the file supplies it. That
// matches remote.LookupEnv, which is the same precedence contract for the
// CLI, and it matters here: everything downstream already treats an empty
// declared value as unset — pick() falls back to the declared default — so
// letting an empty process value shadow the file only means falling through
// to a default connection string, which is how a command reaches the wrong
// server.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		if k != "" {
			if existing, exists := os.LookupEnv(k); !exists || existing == "" {
				os.Setenv(k, v)
			}
		}
	}
}
