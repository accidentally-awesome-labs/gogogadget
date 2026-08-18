package config

import (
	"os"
	"path/filepath"
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

func TestConfiguredPredicates(t *testing.T) {
	baseEnv(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.False(t, cfg.ClerkConfigured())
	assert.False(t, cfg.PolarConfigured())
	assert.False(t, cfg.PostHogEnabled())
	assert.False(t, cfg.SentryEnabled())
	assert.False(t, cfg.ResendConfigured())
	assert.False(t, cfg.StorageConfigured(), "any missing R2 credential ⇒ DevStore")
	assert.False(t, cfg.LLMConfigured(), "needs key AND model")

	t.Setenv("CLERK_SECRET_KEY", "sk_1")
	t.Setenv("POLAR_ACCESS_TOKEN", "pol_1")
	t.Setenv("POSTHOG_API_KEY", "phc_1")
	t.Setenv("SENTRY_DSN", "http://k@h/1")
	t.Setenv("RESEND_API_KEY", "re_1")
	t.Setenv("STORAGE_R2_ACCOUNT_ID", "a")
	t.Setenv("STORAGE_R2_ACCESS_KEY_ID", "b")
	t.Setenv("STORAGE_R2_SECRET_ACCESS_KEY", "c")
	t.Setenv("STORAGE_R2_BUCKET", "d")
	t.Setenv("LLM_API_KEY", "k")
	t.Setenv("LLM_MODEL", "m")
	cfg, err = Load()
	require.NoError(t, err)
	assert.True(t, cfg.ClerkConfigured())
	assert.True(t, cfg.PolarConfigured())
	assert.True(t, cfg.PostHogEnabled())
	assert.True(t, cfg.SentryEnabled())
	assert.True(t, cfg.ResendConfigured())
	assert.True(t, cfg.StorageConfigured())
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
