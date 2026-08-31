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
	"os"
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

	// DEV_AUTH_BYPASS mints synthetic sessions. Booting production with it on
	// would accept forged identities, so it is a hard refusal rather than a warning.
	if cfg.DevAuthBypass && cfg.Production() {
		errs = append(errs, errors.New("DEV_AUTH_BYPASS=true is refused when APP_ENV=production"))
	}

	if cfg.Production() {
		errs = append(errs, requireProductionKeys(lookup)...)
	}

	// Clerk fronts the Frontend API at clerk.<domain> on a production instance
	// and at a wildcard dev host otherwise. Derived from APP_URL rather than
	// asked for, because getting it wrong breaks CSP in a way that is hard to read.
	if cfg.ClerkFrontendAPIURL == "" {
		if cfg.Production() {
			host := strings.TrimPrefix(strings.TrimPrefix(cfg.AppURL, "https://"), "http://")
			cfg.ClerkFrontendAPIURL = "https://clerk." + host
		} else {
			cfg.ClerkFrontendAPIURL = "https://*.clerk.accounts.dev"
		}
	}

	return cfg, errors.Join(errs...)
}

// Load reads the process environment, auto-loading `.env` in development.
func Load() (Config, error) {
	if pick(os.Getenv, "APP_ENV", "development") == "development" {
		loadDotEnv(".env") // missing file is fine; real env wins
	}
	return LoadFrom(os.Getenv)
}

func (c Config) Production() bool       { return c.Env == "production" }
func (c Config) Development() bool      { return c.Env == "development" }
func (c Config) Test() bool             { return c.Env == "test" }
func (c Config) ClerkConfigured() bool { return c.ClerkSecretKey != "" }
func (c Config) PolarConfigured() bool { return c.PolarAccessToken != "" }
func (c Config) PostHogEnabled() bool  { return c.PostHogAPIKey != "" }
func (c Config) SentryEnabled() bool   { return c.SentryDSN != "" }

// LLMConfigured reports whether an OpenAI-compatible backend is set. Empty →
// the AI route renders a 503 not-configured (same degrade as billing).
func (c Config) LLMConfigured() bool {
	return c.LLMAPIKey != "" && c.LLMModel != ""
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
// that are already set. Supports comments, blank lines, optional `export `,
// and single/double quoted values. Intentionally minimal.
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
			if _, exists := os.LookupEnv(k); !exists {
				os.Setenv(k, v)
			}
		}
	}
}
