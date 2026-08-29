package observability

import (
	"context"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
)

// Reporting always has a live seam: unconfigured resolves to the no-op so
// callers never nil-check, and a DSN swaps in Sentry.
func TestNewModuleFallsBackToNoopReporter(t *testing.T) {
	h := apphost.Map(nil, time.Now(), "test")

	m, err := NewModule(context.Background(), h, Deps{Config: &config.Config{}})
	if err != nil {
		t.Fatalf("NewModule(unconfigured): %v", err)
	}
	if _, ok := m.Reporter.(NoopReporter); !ok {
		t.Fatalf("unconfigured reporter = %T, want NoopReporter", m.Reporter)
	}
}

func TestNewModuleRejectsMissingConfig(t *testing.T) {
	h := apphost.Map(nil, time.Now(), "test")
	if _, err := NewModule(context.Background(), h, Deps{}); err == nil {
		t.Fatal("NewModule(nil config) = nil error, want failure")
	}
}
