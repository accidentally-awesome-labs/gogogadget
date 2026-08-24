package flags

import (
	"context"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
)

// The flag evaluator caches flag rows, so the module owns a live object rather
// than a function. The cache TTL is a real behavioral knob: the module fixes it
// so every deployment agrees on how stale a flag decision can be.
func TestNewModuleProvidesEvaluator(t *testing.T) {
	host := apphost.Map(nil, time.Now(), "test")

	module, err := NewModule(context.Background(), host, Deps{Queries: &sqlc.Queries{}})
	if err != nil {
		t.Fatalf("NewModule: %v", err)
	}
	if module.Evaluator == nil {
		t.Fatal("Evaluator = nil")
	}
	if _, ok := module.Evaluator.(*DBEvaluator); !ok {
		t.Fatalf("Evaluator = %T, want *DBEvaluator", module.Evaluator)
	}
}

// Flags are read on request paths, so a missing query handle is a wiring bug
// that must fail at boot rather than nil-panic under traffic.
func TestNewModuleRejectsMissingQueries(t *testing.T) {
	host := apphost.Map(nil, time.Now(), "test")
	if _, err := NewModule(context.Background(), host, Deps{}); err == nil {
		t.Fatal("NewModule(nil queries) = nil error, want failure")
	}
}
