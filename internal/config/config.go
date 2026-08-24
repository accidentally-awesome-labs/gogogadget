// Package config parses and validates environment configuration.
// Stdlib only — no env library. `.env` is auto-loaded in development only,
// via a tiny inline KEY=VALUE parser.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env         string // development | test | production
	AppURL      string
	Port        int
	DatabaseURL string

	ClerkSecretKey      string
	ClerkWebhookSecret  string
	ClerkPortalURL      string // Account Portal base, e.g. https://accounts.example.com
	ClerkPublishableKey string
	ClerkFrontendAPIURL string // Clerk Frontend API origin — feeds CSP connect-src

	AdminEmail string

	PolarAccessToken   string
	PolarWebhookSecret string
	PolarProductPro    string
	PolarProductTeam   string
	PolarServer        string // sandbox | production

	ResendAPIKey string
	EmailFrom    string

	PostHogAPIKey string
	PostHogHost   string

	SentryDSN string

	StorageR2AccountID       string
	StorageR2AccessKeyID     string
	StorageR2SecretAccessKey string
	StorageR2Bucket          string
	StorageR2Endpoint        string // override for AWS S3/MinIO compat; empty = R2 default

	LLMAPIKey  string
	LLMBaseURL string // default https://api.openai.com/v1
	LLMModel   string

	// TEST_NOW (RFC3339) freezes the render clock; honored only when Env == test.
	testNow       time.Time
	hasTestNow    bool
	DevAuthBypass bool
	// MaintenanceMode (MAINTENANCE_MODE=true) sheds all traffic except
	// probes/static via a 503 page; JSON 503 under /api/.
	MaintenanceMode bool
	MetricsToken    string // METRICS_TOKEN: bearer gate for /metrics (prod requires it)
	// RateLimitPerMinute is the per-IP request budget (burst = 2×). Tunable
	// because load profiles differ — the e2e suite drives one IP hard.
	RateLimitPerMinute int
	// APIRateLimitPerMinute is the per-API-token budget (burst = 2×) on
	// /api/v1. Separate from the per-IP shield above because a token is a
	// better identity than an address: it survives NAT and roaming, and it
	// is the thing a customer can rotate when they abuse it.
	APIRateLimitPerMinute int
	// AuditRetentionDays (AUDIT_RETENTION_DAYS): janitor deletes older audit
	// rows; 0 = retain forever.
	AuditRetentionDays int
	LogLevel           string
}

// LoadFrom reads configuration through lookup and validates the result. All
// validation problems are reported together. Modules parse through this so a
// runtime can boot against a fixed environment without touching the process.
func LoadFrom(lookup func(string) string) (Config, error) {
	env := pick(lookup, "APP_ENV", "development")

	cfg := Config{
		Env:         env,
		AppURL:      strings.TrimRight(pick(lookup, "APP_URL", "http://localhost:8080"), "/"),
		DatabaseURL: pick(lookup, "DATABASE_URL", "postgres://postgres:postgres@localhost:5432/gogogadget?sslmode=disable"),

		ClerkSecretKey:      pick(lookup, "CLERK_SECRET_KEY", ""),
		ClerkWebhookSecret:  pick(lookup, "CLERK_WEBHOOK_SECRET", ""),
		ClerkPortalURL:      strings.TrimRight(pick(lookup, "CLERK_PORTAL_URL", ""), "/"),
		ClerkPublishableKey: pick(lookup, "CLERK_PUBLISHABLE_KEY", ""),
		ClerkFrontendAPIURL: pick(lookup, "CLERK_FRONTEND_API_URL", ""),

		AdminEmail: pick(lookup, "ADMIN_EMAIL", ""),

		PolarAccessToken:   pick(lookup, "POLAR_ACCESS_TOKEN", ""),
		PolarWebhookSecret: pick(lookup, "POLAR_WEBHOOK_SECRET", ""),
		PolarProductPro:    pick(lookup, "POLAR_PRODUCT_PRO", ""),
		PolarProductTeam:   pick(lookup, "POLAR_PRODUCT_TEAM", ""),
		PolarServer:        pick(lookup, "POLAR_SERVER", "sandbox"),

		PostHogAPIKey: pick(lookup, "POSTHOG_API_KEY", ""),
		PostHogHost:   pick(lookup, "POSTHOG_HOST", "https://us.i.posthog.com"),

		StorageR2AccountID:       pick(lookup, "STORAGE_R2_ACCOUNT_ID", ""),
		StorageR2AccessKeyID:     pick(lookup, "STORAGE_R2_ACCESS_KEY_ID", ""),
		StorageR2SecretAccessKey: pick(lookup, "STORAGE_R2_SECRET_ACCESS_KEY", ""),
		StorageR2Bucket:          pick(lookup, "STORAGE_R2_BUCKET", ""),
		StorageR2Endpoint:        strings.TrimRight(pick(lookup, "STORAGE_R2_ENDPOINT", ""), "/"),

		LLMAPIKey:       pick(lookup, "LLM_API_KEY", ""),
		LLMBaseURL:      strings.TrimRight(pick(lookup, "LLM_BASE_URL", "https://api.openai.com/v1"), "/"),
		LLMModel:        pick(lookup, "LLM_MODEL", ""),
		SentryDSN:       pick(lookup, "SENTRY_DSN", ""),
		ResendAPIKey:    pick(lookup, "RESEND_API_KEY", ""),
		EmailFrom:       pick(lookup, "EMAIL_FROM", "GoGoGadget <hello@example.com>"),
		MaintenanceMode: parseBool(pick(lookup, "MAINTENANCE_MODE", "")),
		MetricsToken:    pick(lookup, "METRICS_TOKEN", ""),
	}

	cfg.Port = 8080
	if v := pick(lookup, "PORT", ""); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			return Config{}, fmt.Errorf("PORT: %q is not a valid port", v)
		}
		cfg.Port = p
	}

	cfg.RateLimitPerMinute = 100
	if v := pick(lookup, "RATE_LIMIT_RPM", ""); v != "" {
		rpm, err := strconv.Atoi(v)
		if err != nil || rpm < 1 {
			return Config{}, fmt.Errorf("RATE_LIMIT_RPM: %q must be a positive integer", v)
		}
		cfg.RateLimitPerMinute = rpm
	}

	cfg.APIRateLimitPerMinute = 60
	if v := pick(lookup, "API_RATE_LIMIT_RPM", ""); v != "" {
		rpm, err := strconv.Atoi(v)
		if err != nil || rpm < 1 {
			return Config{}, fmt.Errorf("API_RATE_LIMIT_RPM: %q must be a positive integer", v)
		}
		cfg.APIRateLimitPerMinute = rpm
	}

	if v := pick(lookup, "AUDIT_RETENTION_DAYS", ""); v != "" {
		days, err := strconv.Atoi(v)
		if err != nil || days < 0 {
			return Config{}, fmt.Errorf("AUDIT_RETENTION_DAYS: %q must be a non-negative integer", v)
		}
		cfg.AuditRetentionDays = days
	}

	cfg.LogLevel = pick(lookup, "LOG_LEVEL", "")
	if cfg.LogLevel == "" {
		if cfg.Development() {
			cfg.LogLevel = "debug"
		} else {
			cfg.LogLevel = "info"
		}
	}

	var errs []error

	switch cfg.Env {
	case "development", "test", "production":
	default:
		errs = append(errs, fmt.Errorf("APP_ENV: %q must be development, test, or production", cfg.Env))
	}

	// DEV_AUTH_BYPASS enables synthetic e2e: session tokens. It is honored only
	// outside production — booting production with it on is a hard error.
	cfg.DevAuthBypass = parseBool(pick(lookup, "DEV_AUTH_BYPASS", "false"))
	if cfg.DevAuthBypass && cfg.Production() {
		errs = append(errs, errors.New("DEV_AUTH_BYPASS=true is refused when APP_ENV=production"))
	}

	if cfg.Production() {
		// No dev fallback DSN in production: refuse to boot into the wrong database.
		if lookup("DATABASE_URL") == "" {
			errs = append(errs, errors.New("DATABASE_URL is required when APP_ENV=production"))
		}
		for _, k := range []string{"CLERK_SECRET_KEY", "CLERK_WEBHOOK_SECRET", "CLERK_PORTAL_URL", "CLERK_PUBLISHABLE_KEY"} {
			if lookup(k) == "" {
				errs = append(errs, fmt.Errorf("%s is required when APP_ENV=production", k))
			}
		}
	}

	if cfg.ClerkFrontendAPIURL == "" {
		if cfg.Production() {
			// Production Clerk instances front the Frontend API at clerk.<domain>.
			host := strings.TrimPrefix(strings.TrimPrefix(cfg.AppURL, "https://"), "http://")
			cfg.ClerkFrontendAPIURL = "https://clerk." + host
		} else {
			cfg.ClerkFrontendAPIURL = "https://*.clerk.accounts.dev"
		}
	}

	if cfg.PolarServer != "sandbox" && cfg.PolarServer != "production" {
		errs = append(errs, fmt.Errorf("POLAR_SERVER: %q must be sandbox or production", cfg.PolarServer))
	}

	if v := pick(lookup, "TEST_NOW", ""); v != "" && cfg.Env == "test" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			errs = append(errs, fmt.Errorf("TEST_NOW: %v", err))
		} else {
			cfg.testNow, cfg.hasTestNow = t, true
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
func (c Config) ClerkConfigured() bool  { return c.ClerkSecretKey != "" }
func (c Config) PolarConfigured() bool  { return c.PolarAccessToken != "" }
func (c Config) PostHogEnabled() bool   { return c.PostHogAPIKey != "" }
func (c Config) SentryEnabled() bool    { return c.SentryDSN != "" }
func (c Config) ResendConfigured() bool { return c.ResendAPIKey != "" }

// StorageConfigured reports whether R2 (or S3-compatible) credentials are
// present. Unconfigured → DevStore (tmp/uploads), so a fresh clone needs zero
// accounts.
func (c Config) StorageConfigured() bool {
	return c.StorageR2AccountID != "" && c.StorageR2AccessKeyID != "" &&
		c.StorageR2SecretAccessKey != "" && c.StorageR2Bucket != ""
}

// LLMConfigured reports whether an OpenAI-compatible backend is set. Empty →
// the AI route renders a 503 not-configured (same degrade as billing).
func (c Config) LLMConfigured() bool {
	return c.LLMAPIKey != "" && c.LLMModel != ""
}

// Now returns the render clock: frozen at TEST_NOW under APP_ENV=test,
// wall-clock otherwise. All rendered dates/times derive from this so visual
// baselines never rot.
func (c Config) Now() time.Time {
	if c.hasTestNow {
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
