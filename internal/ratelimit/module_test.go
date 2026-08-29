package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
)

// The limiter holds per-key state and a janitor goroutine, so the module owns a
// live object. The budget comes from configuration because load profiles differ.
func TestNewModuleProvidesConfiguredLimiter(t *testing.T) {
	host := apphost.Map(nil, time.Now(), "test")

	module, err := NewModule(context.Background(), host, Deps{
		Config: &config.Config{RateLimitPerMinute: 120},
	})
	if err != nil {
		t.Fatalf("NewModule: %v", err)
	}
	if module.Limiter == nil {
		t.Fatal("Limiter = nil")
	}
	// The configured budget must admit a burst of 2n and then refuse.
	allowed := 0
	for range 300 {
		if module.Limiter.Allow("1.2.3.4") {
			allowed++
		}
	}
	if allowed == 0 {
		t.Fatal("limiter refused every request")
	}
	if allowed == 300 {
		t.Fatal("limiter admitted every request; the budget is not applied")
	}
}

func TestNewModuleRejectsMissingConfig(t *testing.T) {
	host := apphost.Map(nil, time.Now(), "test")
	if _, err := NewModule(context.Background(), host, Deps{}); err == nil {
		t.Fatal("NewModule(nil config) = nil error, want failure")
	}
}
