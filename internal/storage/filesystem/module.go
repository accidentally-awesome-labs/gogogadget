// Package filesystem implements the local filesystem storage adapter.
package filesystem

import (
	"context"
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

func NewModule(ctx context.Context, _ apphost.Host, d Deps) (*Module, error) {
	if d.Config == nil {
		return nil, fmt.Errorf("storage filesystem: config dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Module{Store: NewDevStore("tmp/uploads")}, nil
}

var _ storage.Store = (*DevStore)(nil)
