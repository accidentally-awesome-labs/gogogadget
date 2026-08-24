package modules

import (
	"context"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/analytics"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/mail"
	"github.com/gogogadget/gogogadget/internal/observability"
	"github.com/gogogadget/gogogadget/internal/storage"
)

// bootHost is a fully unconfigured environment: the state a fresh clone boots
// in. Only the settings a fresh clone actually has are present.
func bootHost() apphost.Host {
	return apphost.Map(map[string]string{
		"APP_ENV": "test",
		"APP_URL": "http://localhost:8080",
	}, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "v-test")
}

// Boot is generated from the selected module graph, so this is the test that
// proves the generated wiring is real: an unconfigured host must produce a
// runtime whose every capability is the documented local fallback.
func TestBootWiresUnconfiguredFallbacks(t *testing.T) {
	runtime, err := Boot(context.Background(), bootHost(), Options{})
	if err != nil {
		t.Fatalf("Boot: %v", err)
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
	host := apphost.Map(map[string]string{
		"APP_ENV":         "test",
		"APP_URL":         "http://localhost:8080",
		"DEV_AUTH_BYPASS": "true",
		"RESEND_API_KEY":  "re_test",
		"EMAIL_FROM":      "hello@example.com",
	}, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "v-test")

	runtime, err := Boot(context.Background(), host, Options{})
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
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
