package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Load is environment-driven: every test pins the whole relevant env via
// t.Setenv (auto-restored) and a .env is never loaded because APP_ENV is
// always set to test explicitly.
func baseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "test")
	t.Setenv("DATABASE_URL", "postgres://test")
}

func TestLoadMinimalValidEnv(t *testing.T) {
	baseEnv(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "test", cfg.Env)
	assert.Equal(t, 8080, cfg.Port)
	assert.Equal(t, "info", cfg.LogLevel, "non-development defaults to info")
	assert.False(t, cfg.MaintenanceMode)
	assert.False(t, cfg.DevAuthBypass)
}

func TestLoadInvalidAppEnv(t *testing.T) {
	baseEnv(t)
	t.Setenv("APP_ENV", "staging")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `APP_ENV: "staging"`)
}

func TestLoadInvalidPort(t *testing.T) {
	baseEnv(t)
	t.Setenv("PORT", "notaport")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PORT")

	t.Setenv("PORT", "70000")
	_, err = Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PORT")
}

func TestLoadInvalidLogLevelIsNotValidated(t *testing.T) {
	// Observed behavior: LOG_LEVEL is not validated at Load; the logger
	// builder maps unknown levels to info (see newLogger). Pin the contract.
	baseEnv(t)
	t.Setenv("LOG_LEVEL", "shouty")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "shouty", cfg.LogLevel)
}

func TestLoadDevAuthBypassRefusedInProduction(t *testing.T) {
	baseEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("DEV_AUTH_BYPASS", "true")
	for _, k := range []string{"CLERK_SECRET_KEY", "CLERK_WEBHOOK_SECRET", "CLERK_PORTAL_URL", "CLERK_PUBLISHABLE_KEY"} {
		t.Setenv(k, "x")
	}
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DEV_AUTH_BYPASS=true is refused")
}

func TestLoadProductionRequiresDatabaseAndClerk(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_URL is required")
	assert.Contains(t, err.Error(), "CLERK_SECRET_KEY is required")
}

func TestLoadMaintenanceModeParsing(t *testing.T) {
	baseEnv(t)
	t.Setenv("MAINTENANCE_MODE", "TRUE")
	cfg, err := Load()
	require.NoError(t, err)
	assert.True(t, cfg.MaintenanceMode, "parseBool is case-insensitive")

	t.Setenv("MAINTENANCE_MODE", "off")
	cfg, err = Load()
	require.NoError(t, err)
	assert.False(t, cfg.MaintenanceMode, "off/on are boolean words, not truthy strings")
}

func TestTestNowFreezesClock(t *testing.T) {
	baseEnv(t)
	t.Setenv("TEST_NOW", "2026-01-15T00:00:00Z")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), cfg.Now())

	// TEST_NOW outside APP_ENV=test is ignored: wall clock stays live.
	t.Setenv("APP_ENV", "development")
	cfg, err = Load()
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), cfg.Now(), 2*time.Second)

	t.Setenv("APP_ENV", "test")
	t.Setenv("TEST_NOW", "not-a-time")
	_, err = Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TEST_NOW")
}

func TestProductionAggregatesManagedProviderCredentials(t *testing.T) {
	baseEnv(t)
	t.Setenv("APP_ENV", "production")
	for _, key := range []string{"RESEND_API_KEY", "STORAGE_R2_ACCESS_KEY_ID", "STORAGE_R2_ACCOUNT_ID", "STORAGE_R2_BUCKET", "STORAGE_R2_SECRET_ACCESS_KEY"} {
		t.Setenv(key, "")
	}
	_, err := Load()
	require.Error(t, err)
	for _, key := range []string{"RESEND_API_KEY", "STORAGE_R2_ACCESS_KEY_ID", "STORAGE_R2_ACCOUNT_ID", "STORAGE_R2_BUCKET", "STORAGE_R2_SECRET_ACCESS_KEY"} {
		assert.Contains(t, err.Error(), key)
	}
	assert.NotContains(t, err.Error(), "re_test")
}

func TestAuditRetentionDaysParsing(t *testing.T) {
	baseEnv(t)
	t.Setenv("AUDIT_RETENTION_DAYS", "365")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 365, cfg.AuditRetentionDays)

	t.Setenv("AUDIT_RETENTION_DAYS", "") // unset → 0 = retain forever
	cfg, err = Load()
	require.NoError(t, err)
	assert.Zero(t, cfg.AuditRetentionDays)

	t.Setenv("AUDIT_RETENTION_DAYS", "-7")
	_, err = Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AUDIT_RETENTION_DAYS")

	t.Setenv("AUDIT_RETENTION_DAYS", "soon")
	_, err = Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AUDIT_RETENTION_DAYS")
}

func TestMetricsTokenLoadsFromEnv(t *testing.T) {
	baseEnv(t)
	t.Setenv("METRICS_TOKEN", "tok-1")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "tok-1", cfg.MetricsToken, "METRICS_TOKEN must reach Config — guards the wiring, not just the field")
}

func TestRateLimitRPMParsing(t *testing.T) {
	baseEnv(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 100, cfg.RateLimitPerMinute, "production-safe default")

	t.Setenv("RATE_LIMIT_RPM", "100000")
	cfg, err = Load()
	require.NoError(t, err)
	assert.Equal(t, 100000, cfg.RateLimitPerMinute)

	for _, bad := range []string{"0", "-5", "lots"} {
		t.Setenv("RATE_LIMIT_RPM", bad)
		_, err = Load()
		require.Error(t, err, bad)
		assert.Contains(t, err.Error(), "RATE_LIMIT_RPM")
	}
}

func TestConfiguredPredicates(t *testing.T) {
	baseEnv(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.False(t, cfg.PostHogEnabled())
	assert.False(t, cfg.SentryEnabled())
	assert.False(t, cfg.LLMConfigured(), "needs key AND model")

	t.Setenv("CLERK_SECRET_KEY", "sk_1")
	t.Setenv("POLAR_ACCESS_TOKEN", "pol_1")
	t.Setenv("POSTHOG_API_KEY", "phc_1")
	t.Setenv("SENTRY_DSN", "http://k@h/1")
	t.Setenv("STORAGE_R2_ACCOUNT_ID", "a")
	t.Setenv("STORAGE_R2_ACCESS_KEY_ID", "b")
	t.Setenv("STORAGE_R2_SECRET_ACCESS_KEY", "c")
	t.Setenv("STORAGE_R2_BUCKET", "d")
	t.Setenv("LLM_API_KEY", "k")
	t.Setenv("LLM_MODEL", "m")
	cfg, err = Load()
	require.NoError(t, err)
	assert.True(t, cfg.PostHogEnabled())
	assert.True(t, cfg.SentryEnabled())
	assert.True(t, cfg.LLMConfigured())
}

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(path, []byte(`
# comment line
PLAIN=value
QUOTED="double quoted"
SINGLE='single quoted'
export EXPORTED=yes
EXISTING=fromfile
=ignored-key-empty
no-equals-line
`), 0o600))

	t.Setenv("EXISTING", "fromenv")
	loadDotEnv(path)

	assert.Equal(t, "value", os.Getenv("PLAIN"))
	assert.Equal(t, "double quoted", os.Getenv("QUOTED"), "double quotes stripped")
	assert.Equal(t, "single quoted", os.Getenv("SINGLE"), "single quotes stripped")
	assert.Equal(t, "yes", os.Getenv("EXPORTED"), "export prefix stripped")
	assert.Equal(t, "fromenv", os.Getenv("EXISTING"), "real env never overridden")
	assert.Empty(t, os.Getenv("no-equals-line"))

	t.Run("missing file is fine", func(t *testing.T) {
		assert.NotPanics(t, func() { loadDotEnv(filepath.Join(dir, "nope")) })
	})
}

func TestAPIRateLimitRPMParsing(t *testing.T) {
	baseEnv(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 60, cfg.APIRateLimitPerMinute, "unset → the documented default, never 0 (which would mean no budget)")

	t.Setenv("API_RATE_LIMIT_RPM", "600")
	cfg, err = Load()
	require.NoError(t, err)
	assert.Equal(t, 600, cfg.APIRateLimitPerMinute)

	for _, bad := range []string{"0", "-5", "lots"} {
		t.Setenv("API_RATE_LIMIT_RPM", bad)
		_, err = Load()
		require.Error(t, err, "%q must be rejected at boot, not silently become a zero budget", bad)
		assert.Contains(t, err.Error(), "API_RATE_LIMIT_RPM")
	}
}

// Every validation problem is reported together. Fixing one misconfiguration,
// re-running, and discovering the next is the failure mode this prevents — and
// it is exactly what an early return produces.
func TestLoadFromAggregatesEveryValidationError(t *testing.T) {
	env := map[string]string{
		"APP_ENV":              "staging", // not a known environment
		"PORT":                 "abc",     // not a number
		"RATE_LIMIT_RPM":       "0",       // below the minimum
		"API_RATE_LIMIT_RPM":   "-5",      // below the minimum
		"AUDIT_RETENTION_DAYS": "-1",      // below the minimum
		"POLAR_SERVER":         "staging", // not a known server
	}
	_, err := LoadFrom(func(k string) string { return env[k] })
	if err == nil {
		t.Fatal("LoadFrom accepted six invalid values")
	}
	message := err.Error()
	for key := range env {
		if !strings.Contains(message, key) {
			t.Fatalf("error omits %s, so fixing the others would not be enough:\n%s", key, message)
		}
	}
}
func TestLoadFromValuesUseNormalizedDefaults(t *testing.T) {
	cfg, err := LoadFrom(func(key string) string {
		if key == "APP_ENV" {
			return "test"
		}
		return ""
	})
	require.NoError(t, err)
	require.Equal(t, "8080", cfg.Value("PORT"))
	require.Equal(t, 8080, cfg.Port)
}

// Reported in declaration order, so the same broken environment always produces
// the same message — a diffable one.
func TestLoadFromReportsErrorsInDeclarationOrder(t *testing.T) {
	env := map[string]string{"PORT": "abc", "RATE_LIMIT_RPM": "0", "APP_ENV": "staging"}
	first, err := LoadFrom(func(k string) string { return env[k] })
	if err == nil {
		t.Fatal("expected errors")
	}
	second, err2 := LoadFrom(func(k string) string { return env[k] })
	if err2 == nil || err.Error() != err2.Error() {
		t.Fatalf("error order is unstable:\n%s\n---\n%v", err.Error(), err2)
	}
	_, _ = first, second

	order := []string{}
	for _, key := range ConfigRegistry {
		if idx := strings.Index(err.Error(), key); idx >= 0 {
			order = append(order, key)
		}
	}
	if len(order) < 3 {
		t.Fatalf("expected all three keys named, got %v", order)
	}
	positions := []int{}
	for _, key := range order {
		positions = append(positions, strings.Index(err.Error(), key))
	}
	for i := 1; i < len(positions); i++ {
		if positions[i] < positions[i-1] {
			t.Fatalf("errors are not in declaration order: %v at %v", order, positions)
		}
	}
}

// AGENTS.md promises a fresh clone runs end-to-end with no third-party
// accounts, and .env.example is the file that has to deliver it. Generating the
// file from declarations put that promise at risk, so it is asserted directly:
// the shipped example must be a valid configuration that signs a developer in.
func TestEnvExampleIsAWorkingZeroAccountSetup(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".env.example"))
	require.NoError(t, err)

	values := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if key, value, ok := strings.Cut(line, "="); ok {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}

	cfg, err := LoadFrom(func(k string) string { return values[k] })
	require.NoError(t, err, ".env.example must itself be a valid configuration")

	assert.True(t, cfg.DevAuthBypass,
		"DEV_AUTH_BYPASS must ship on, or /dev/login cannot sign anyone in on a fresh clone")
	assert.Contains(t, cfg.DatabaseURL, "localhost",
		"DATABASE_URL must ship pointing at the compose database")
	assert.True(t, cfg.Development(), "the shipped example must be a development configuration")

	// The credential-bearing keys must ship blank: a plausible-looking fake
	// secret is the kind of thing that reaches production.
	for _, key := range []string{
		"CLERK_SECRET_KEY", "CLERK_WEBHOOK_SECRET", "POLAR_ACCESS_TOKEN",
		"POLAR_WEBHOOK_SECRET", "RESEND_API_KEY", "SENTRY_DSN",
		"STORAGE_R2_SECRET_ACCESS_KEY", "LLM_API_KEY", "METRICS_TOKEN",
	} {
		assert.Empty(t, values[key], "%s must ship without a value", key)
	}

	// Every declared key appears, so a reader is never left guessing that a
	// setting exists. This is the drift that shipped five keys short.
	for _, key := range ConfigRegistry {
		_, present := values[key]
		assert.True(t, present, "%s is declared but missing from .env.example", key)
	}
}
