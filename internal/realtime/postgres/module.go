package postgres

import (
	"context"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/realtime"
)

type Deps struct{}
type Module struct{ Value *Broker }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	return &Module{Value: New(nil)}, ctx.Err()
}
func (m *Module) Health(ctx context.Context) error {
	if m == nil || m.Value == nil {
		return fmt.Errorf("postgres realtime: broker is required")
	}
	return m.Value.Health(ctx)
}

var _ realtime.Broker = (*Broker)(nil)
