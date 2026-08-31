package storages3

import (
	"context"
	"io"
	"net/http"
	"fmt"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/storage"
)

type Deps struct {
	Config *config.Config
}

type Module struct {
	Store storage.Store
}

type store struct{}

func (store) Put(ctx context.Context, _ string, _ string, r io.Reader) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return io.Copy(io.Discard, r)
}
func (store) Serve(ctx context.Context, _ http.ResponseWriter, _, _, _ string) error { return ctx.Err() }
func (store) ServeInline(ctx context.Context, _ http.ResponseWriter, _, _ string) error { return ctx.Err() }
func (store) Delete(ctx context.Context, _ string) error { return ctx.Err() }

func NewModule(ctx context.Context, _ apphost.Host, deps Deps) (*Module, error) {
	if deps.Config == nil {
		return nil, fmt.Errorf("managed storage fixture: config dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Module{Store: store{}}, nil
}
func (*Module) Health(ctx context.Context) error { return ctx.Err() }

var _ storage.Store = store{}
var _ apphost.HealthChecker = (*Module)(nil)
