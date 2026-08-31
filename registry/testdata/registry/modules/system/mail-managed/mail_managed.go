package mailmanaged

import (
	"context"
	"fmt"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/mail"
)

type Deps struct {
	Config *config.Config
}

type Module struct {
	Sender mail.Sender
}

type sender struct{}

func (sender) Send(ctx context.Context, _ mail.Message) error { return ctx.Err() }

func NewModule(ctx context.Context, _ apphost.Host, deps Deps) (*Module, error) {
	if deps.Config == nil {
		return nil, fmt.Errorf("managed mail fixture: config dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Module{Sender: sender{}}, nil
}

func (*Module) Health(ctx context.Context) error { return ctx.Err() }

var _ mail.Sender = sender{}
var _ apphost.HealthChecker = (*Module)(nil)
