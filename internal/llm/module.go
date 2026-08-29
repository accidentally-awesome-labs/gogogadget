// Package-level module wiring. Deps/NewModule is the uniform constructor shape
// the generated bootstrap calls.
package llm

import (
	"context"
	"fmt"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
)

// Deps is the typed dependency set the generated bootstrap supplies.
type Deps struct {
	Config *config.Config
}

// Module is the constructed LLM closure. Completer is nil when unconfigured:
// there is no local stand-in for a model, so AI routes answer 503.
type Module struct {
	Completer Completer
}

// NewModule selects an OpenAI-compatible client when configured and leaves the
// port nil otherwise.
func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if d.Config == nil {
		return nil, fmt.Errorf("llm: config dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	log := h.Log()
	if !d.Config.LLMConfigured() {
		log.Warn("llm not configured — AI routes will 503")
		return &Module{}, nil
	}
	log.Info("llm: openai-compatible", "model", d.Config.LLMModel, "base", d.Config.LLMBaseURL)
	return &Module{Completer: NewOpenAICompat(d.Config.LLMBaseURL, d.Config.LLMAPIKey, d.Config.LLMModel)}, nil
}
