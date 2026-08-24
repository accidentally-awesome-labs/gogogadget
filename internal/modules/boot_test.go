package modules

import (
	"context"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/analytics"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/db/testdb"
	"github.com/gogogadget/gogogadget/internal/mail"
	"github.com/gogogadget/gogogadget/internal/observability"
	"github.com/gogogadget/gogogadget/internal/storage"
)

// bootHost is an otherwise unconfigured environment pointed at an empty scratch
// database: the state a fresh clone boots in. The database is real because the
// runtime genuinely needs one — every request path depends on it — so a fake
// would prove nothing about Boot. Skips when no server is reachable.
func bootHost(t *testing.T, name string, extra map[string]string) apphost.Host {
	t.Helper()
	env := map[string]string{
		"APP_ENV":      "test",
		"APP_URL":      "http://localhost:8080",
		"DATABASE_URL": testdb.DSN(t, name),
	}
	for k, v := range extra {
		env[k] = v
	}
	return apphost.Map(env, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "v-test")
}

// closeRuntime asserts the generated shutdown path actually runs.
func closeRuntime(t *testing.T, runtime *Runtime) {
	t.Helper()
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// Boot is generated from the selected module graph, so this is the test that
// proves the generated wiring is real: an unconfigured host must produce a
// runtime whose every capability is the documented local fallback.
func TestBootWiresUnconfiguredFallbacks(t *testing.T) {
	runtime, err := Boot(context.Background(), bootHost(t, "boot_fallbacks", nil), Options{})
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	t.Cleanup(func() { closeRuntime(t, runtime) })

	// The database module opened a pool and ran migrations as part of Boot.
	if runtime.DatabasePool == nil {
		t.Fatal("DatabasePool capability is nil")
	}
	if runtime.DatabaseQueries == nil {
		t.Fatal("DatabaseQueries capability is nil")
	}
	if err := runtime.DatabasePool.Ping(context.Background()); err != nil {
		t.Fatalf("booted pool is unusable: %v", err)
	}

	if runtime.Config == nil {
		t.Fatal("Config capability is nil")
	}
	if got := runtime.Config.Env; got != "test" {
		t.Fatalf("Config.Env = %q, want %q", got, "test")
	}

	// Ports with a local stand-in must be live.
	if _, ok := runtime.MailSender.(*mail.DevSender); !ok {
		t.Fatalf("MailSender = %T, want *mail.DevSender", runtime.MailSender)
	}
	if _, ok := runtime.StorageStore.(*storage.DevStore); !ok {
		t.Fatalf("StorageStore = %T, want *storage.DevStore", runtime.StorageStore)
	}
	if _, ok := runtime.ObservabilityReporter.(observability.NoopReporter); !ok {
		t.Fatalf("ObservabilityReporter = %T, want observability.NoopReporter", runtime.ObservabilityReporter)
	}
	if _, ok := runtime.AnalyticsCapturer.(analytics.NoopCapturer); !ok {
		t.Fatalf("AnalyticsCapturer = %T, want analytics.NoopCapturer", runtime.AnalyticsCapturer)
	}

	// Ports with no local stand-in must stay nil so their routes answer 503
	// rather than pretending a provider exists.
	if runtime.BillingClient != nil {
		t.Fatalf("BillingClient = %T, want nil when unconfigured", runtime.BillingClient)
	}
	if runtime.LlmCompleter != nil {
		t.Fatalf("LlmCompleter = %T, want nil when unconfigured", runtime.LlmCompleter)
	}
	if runtime.IdentityVerifier != nil {
		t.Fatalf("IdentityVerifier = %T, want nil when unconfigured", runtime.IdentityVerifier)
	}
}

// Configuration reaches the modules through the host, so flipping one host value
// must change which adapter the generated graph selects.
func TestBootSelectsConfiguredAdapters(t *testing.T) {
	host := bootHost(t, "boot_configured", map[string]string{
		"DEV_AUTH_BYPASS": "true",
		"RESEND_API_KEY":  "re_test",
		"EMAIL_FROM":      "hello@example.com",
	})

	runtime, err := Boot(context.Background(), host, Options{})
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	t.Cleanup(func() { closeRuntime(t, runtime) })
	if _, ok := runtime.MailSender.(*mail.ResendSender); !ok {
		t.Fatalf("MailSender = %T, want *mail.ResendSender", runtime.MailSender)
	}
	if runtime.IdentityVerifier == nil {
		t.Fatal("IdentityVerifier = nil, want the dev bypass verifier")
	}
}

// A configuration error must abort the boot naming the module that refused,
// rather than yielding a half-built runtime.
func TestBootFailsClosedOnInvalidConfiguration(t *testing.T) {
	host := apphost.Map(map[string]string{
		"APP_ENV":         "production",
		"APP_URL":         "https://example.com",
		"DEV_AUTH_BYPASS": "true",
	}, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "v-test")

	runtime, err := Boot(context.Background(), host, Options{})
	if err == nil {
		t.Fatal("Boot(production bypass) = nil error, want refusal")
	}
	if runtime != nil {
		t.Fatal("Boot returned a runtime alongside an error")
	}
}
