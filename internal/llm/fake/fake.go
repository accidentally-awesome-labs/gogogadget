package fake

import (
	"context"
	"github.com/gogogadget/gogogadget/internal/llm"
)

type Completer struct{}

func (Completer) Chat(ctx context.Context, r llm.ChatRequest) (llm.ChatResponse, error) {
	if err := ctx.Err(); err != nil {
		return llm.ChatResponse{}, err
	}
	return llm.ChatResponse{Content: "", Model: "fake"}, nil
}

type Module struct{ Value Completer }
type Deps struct{}

func NewModule(ctx context.Context, _ any, _ Deps) (*Module, error) { return &Module{}, ctx.Err() }

var _ llm.Completer = Completer{}

func (m *Module) Health(ctx context.Context) error { return ctx.Err() }
