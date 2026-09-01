package typesense

import (
	"context"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/search"
)

type Deps struct{}
type Module struct{ Value *Index }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	endpoint := h.Env("TYPESENSE_URL")
	if endpoint == "" {
		return nil, fmt.Errorf("typesense: TYPESENSE_URL is required")
	}
	return &Module{Value: New(endpoint, h.Env("TYPESENSE_API_KEY"))}, nil
}
func (m *Module) Health(ctx context.Context) error {
	if m == nil || m.Value == nil {
		return fmt.Errorf("typesense: index is required")
	}
	return m.Value.Health(ctx)
}

var _ search.Index = (*Index)(nil)
