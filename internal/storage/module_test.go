package storage

import (
	"context"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
)

// Unconfigured storage must land in tmp/uploads so a fresh clone can accept
// uploads without an R2 account. Configuration flips the same seam to R2.
func TestNewModuleFallsBackToDevStore(t *testing.T) {
	h := apphost.Map(nil, time.Now(), "test")

	m, err := NewModule(context.Background(), h, Deps{Config: &config.Config{}})
	if err != nil {
		t.Fatalf("NewModule(unconfigured): %v", err)
	}
	if _, ok := m.Store.(*DevStore); !ok {
		t.Fatalf("unconfigured store = %T, want *DevStore", m.Store)
	}
}

func TestNewModuleRejectsMissingConfig(t *testing.T) {
	h := apphost.Map(nil, time.Now(), "test")
	if _, err := NewModule(context.Background(), h, Deps{}); err == nil {
		t.Fatal("NewModule(nil config) = nil error, want failure")
	}
}
