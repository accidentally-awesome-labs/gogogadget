package openai

import (
	"context"
	"fmt"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/llm"
)

type Module struct{ Value llm.Completer }
type Deps struct{ Endpoint, APIKey, Model string }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if h != nil {
		if d.Endpoint == "" {
			d.Endpoint = h.Env("LLM_BASE_URL")
		}
		if d.APIKey == "" {
			d.APIKey = h.Env("LLM_API_KEY")
		}
		if d.Model == "" {
			d.Model = h.Env("LLM_MODEL")
		}
	}
	if d.Endpoint == "" || d.APIKey == "" || d.Model == "" {
		return nil, fmt.Errorf("openai: endpoint, api key, and model are required")
	}
	return &Module{Value: llm.NewOpenAICompat(d.Endpoint, d.APIKey, d.Model)}, nil
}

func (m *Module) Health(ctx context.Context) error {
	if m == nil || m.Value == nil {
		return fmt.Errorf("openai: completer is required")
	}
	return nil
}
