package maillocal

import (
	"context"

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
		return nil, context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Module{Sender: sender{}}, nil
}

var _ mail.Sender = sender{}
