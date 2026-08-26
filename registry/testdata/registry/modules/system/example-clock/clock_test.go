package clock

import (
	"context"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
)

// The system module's own contract test: the declared environment key reaches
// the constructor through the generated config struct, and the provided port
// answers with the host clock plus that skew.
func TestExampleClockAppliesDeclaredSkew(t *testing.T) {
	at := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	host := apphost.Map(map[string]string{}, at, "test")

	module, err := New(context.Background(), host, Deps{Config: &config.Config{ExampleClockSkewSeconds: 90}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := module.Clock.Now(); !got.Equal(at.Add(90 * time.Second)) {
		t.Fatalf("Now() = %s, want %s", got, at.Add(90*time.Second))
	}

	if err := module.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := module.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := module.Stop(context.Background()); err != nil {
		t.Fatalf("Stop is not idempotent: %v", err)
	}
}
