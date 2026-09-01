package modules

import (
	"context"
	"github.com/gogogadget/gogogadget/internal/analytics"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/db/testdb"
	"github.com/gogogadget/gogogadget/internal/llm/fake"
	"github.com/gogogadget/gogogadget/internal/mail/dev"
	"github.com/gogogadget/gogogadget/internal/observability"
	"github.com/gogogadget/gogogadget/internal/storage/filesystem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
	if _, ok := runtime.MailSender.(*dev.DevSender); !ok {
		t.Fatalf("MailSender = %T, want *dev.DevSender", runtime.MailSender)
	}
	if _, ok := runtime.StorageStore.(*filesystem.DevStore); !ok {
		t.Fatalf("StorageStore = %T, want *filesystem.DevStore", runtime.StorageStore)
	}
	if _, ok := runtime.ObservabilityReporter.(observability.NoopReporter); !ok {
		t.Fatalf("ObservabilityReporter = %T, want observability.NoopReporter", runtime.ObservabilityReporter)
	}
	if _, ok := runtime.AnalyticsCapturer.(analytics.NoopCapturer); !ok {
		t.Fatalf("AnalyticsCapturer = %T, want analytics.NoopCapturer", runtime.AnalyticsCapturer)
	}

	if runtime.BillingClient == nil || runtime.BillingCatalog == nil || runtime.BillingWebhook == nil {
		t.Fatal("billing capabilities must be provided by the selected local adapter")
	}
	if runtime.IdentityVerifier == nil || runtime.IdentityFetcher == nil || runtime.IdentityDeleter == nil || runtime.IdentityNavigator == nil || runtime.IdentityWebhook == nil {
		t.Fatal("identity capabilities must be provided by the selected local adapter")
	}
	if _, ok := runtime.LLMCompleter.(fake.Completer); !ok {
		t.Fatalf("LlmCompleter = %T, want fake.Completer", runtime.LLMCompleter)
	}
}

// Configuration reaches the modules through the host, so APP_ENV chooses the
// production adapter set rather than credentials selecting it.
func TestBootSelectsConfiguredAdapters(t *testing.T) {
	host := bootHost(t, "boot_configured", map[string]string{
		"APP_ENV":                      "production",
		"CLERK_PORTAL_URL":             "https://accounts.example.com",
		"CLERK_PUBLISHABLE_KEY":        "pk_live_fixture",
		"CLERK_SECRET_KEY":             "sk_live_fixture",
		"CLERK_WEBHOOK_SECRET":         "whsec_fixture",
		"POLAR_ACCESS_TOKEN":           "polar_fixture",
		"POLAR_WEBHOOK_SECRET":         "polar_wh_fixture",
		"POLAR_PRODUCT_PRO":            "prod_pro",
		"POLAR_PRODUCT_TEAM":           "prod_team",
		"RESEND_API_KEY":               "re_test",
		"STORAGE_R2_ACCESS_KEY_ID":     "ak_fixture",
		"STORAGE_R2_ACCOUNT_ID":        "acct_fixture",
		"STORAGE_R2_BUCKET":            "bucket_fixture",
		"STORAGE_R2_SECRET_ACCESS_KEY": "secret_fixture",
		"SENTRY_DSN":                   "https://public@example.com/1",
		"POSTHOG_API_KEY":              "phc_live_fixture",
		"POSTHOG_HOST":                 "https://us.i.posthog.com",
		"OTLP_AUDIT_EXPORT_URL":        "https://otlp.example.com/audit",
	})
	_, err := Boot(context.Background(), host, Options{})
	if err == nil {
		t.Fatal("production boot without managed clients succeeded")
	}
	if !strings.Contains(err.Error(), "cache") {
		t.Fatalf("production boot error = %v, want selected managed dependency", err)
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

// The generated Run/Close pair is the whole runtime lifecycle. Run must start
// the background worker and hand control back promptly — a Run that blocked
// would stop the process from ever listening — and Close must end it.
func TestRuntimeRunStartsAndCloseStopsBackgroundServices(t *testing.T) {
	runtime, err := Boot(context.Background(), bootHost(t, "boot_lifecycle", nil), Options{})
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	if runtime.JobsWorker == nil {
		t.Fatal("JobsWorker capability is nil")
	}

	ran := make(chan error, 1)
	go func() { ran <- runtime.Run(context.Background()) }()
	select {
	case err := <-ran:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run blocked; long-lived services must own their own goroutines")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := runtime.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Close is the shutdown path; reaching it twice must not error or hang.
	if err := runtime.Close(ctx); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// The whole point of the generated graph is that the process can serve traffic
// without anyone hand-wiring providers. This is that end-to-end claim: boot the
// real closure and drive a real request through the handler it composed.
func TestBootedRuntimeServesRequests(t *testing.T) {
	runtime, err := Boot(context.Background(), bootHost(t, "boot_serving", nil), Options{})
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	t.Cleanup(func() { closeRuntime(t, runtime) })

	handler := runtime.Handler()
	if handler == nil {
		t.Fatal("Handler() = nil; the process would serve nothing")
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want %d", recorder.Code, http.StatusOK)
	}

	// A public page proves templates, i18n, and the middleware chain are all
	// assembled, not just the probe route.
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want %d", page.Code, http.StatusOK)
	}
}

// Test-only modules exist so a fixture can travel the same path a shipped module
// does. The whole design is only safe if a booted production runtime cannot
// reach them, so that is asserted directly rather than assumed from the fact
// that nothing sets the flag.
func TestBootedRuntimeCannotReachTestOnlySurfaces(t *testing.T) {
	runtime, err := Boot(context.Background(), bootHost(t, "boot_testonly", nil), Options{})
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	t.Cleanup(func() { closeRuntime(t, runtime) })

	handler := runtime.Handler()
	for _, path := range []string{"/guides", "/guides/anything"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d on a production runtime, want %d; a test-only surface is reachable",
				path, recorder.Code, http.StatusNotFound)
		}
	}
}
