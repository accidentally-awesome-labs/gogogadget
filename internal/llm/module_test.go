package llm

import (
	"context"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
)

// LLM has no local fallback: unconfigured must yield a nil Completer so AI
// routes answer 503 instead of pretending a model exists.
func TestNewModuleLeavesCompleterNilWhenUnconfigured(t *testing.T) {
	h := apphost.Map(nil, time.Now(), "test")

	m, err := NewModule(context.Background(), h, Deps{Config: &config.Config{}})
	if err != nil {
		t.Fatalf("NewModule(unconfigured): %v", err)
	}
	if m.Completer != nil {
		t.Fatalf("unconfigured completer = %T, want nil", m.Completer)
	}

	configured, err := NewModule(context.Background(), h, Deps{Config: &config.Config{
		LLMAPIKey:  "sk-test",
		LLMBaseURL: "https://api.example.com/v1",
		LLMModel:   "gpt-test",
	}})
	if err != nil {
		t.Fatalf("NewModule(configured): %v", err)
	}
	if configured.Completer == nil {
		t.Fatal("configured completer = nil, want a client")
	}
}

func TestNewModuleRejectsMissingConfig(t *testing.T) {
	h := apphost.Map(nil, time.Now(), "test")
	if _, err := NewModule(context.Background(), h, Deps{}); err == nil {
		t.Fatal("NewModule(nil config) = nil error, want failure")
	}
}
