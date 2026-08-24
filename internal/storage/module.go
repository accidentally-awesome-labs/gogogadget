// Package-level module wiring. Deps/NewModule is the uniform constructor shape
// the generated bootstrap calls; it is the one place that decides which object
// store this deployment runs.
package storage

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

// Module is the constructed storage closure.
type Module struct {
	Store Store
}

// NewModule selects R2 when credentials are configured and the dev store
// (tmp/uploads) otherwise, so uploads work in a fresh clone.
func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if d.Config == nil {
		return nil, fmt.Errorf("storage: config dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	log := h.Log()
	if d.Config.StorageConfigured() {
		r2, err := NewR2Store(ctx,
			d.Config.StorageR2AccountID,
			d.Config.StorageR2AccessKeyID,
			d.Config.StorageR2SecretAccessKey,
			d.Config.StorageR2Bucket,
			d.Config.StorageR2Endpoint,
		)
		if err != nil {
			return nil, fmt.Errorf("r2 storage init: %w", err)
		}
		log.Info("storage: r2", "bucket", d.Config.StorageR2Bucket)
		return &Module{Store: r2}, nil
	}
	log.Info("storage: dev store (tmp/uploads)")
	return &Module{Store: NewDevStore("tmp/uploads")}, nil
}
