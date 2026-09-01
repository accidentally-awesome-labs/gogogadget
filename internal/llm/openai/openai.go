package openai

import (
	"context"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/llm"
)

type Module struct{ Value llm.Completer }
type Deps struct{ Endpoint, APIKey, Model string }

func NewModule(ctx context.Context, _ any, d Deps) (*Module, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Module{Value: llm.NewOpenAICompat(d.Endpoint, d.APIKey, d.Model)}, nil
}

func (m *Module) Health(ctx context.Context) error {
	if m == nil || m.Value == nil {
		return fmt.Errorf("openai: completer is required")
	}
	return nil
}
