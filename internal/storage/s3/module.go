// Package s3 implements the S3-compatible object-storage adapter.
package s3

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
		return nil, fmt.Errorf("storage s3: config dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Config.StorageR2AccessKeyID == "" || d.Config.StorageR2SecretAccessKey == "" || d.Config.StorageR2Bucket == "" {
		return nil, fmt.Errorf("storage s3: storage access key, secret, and bucket are required")
	}
	store, err := NewR2Store(ctx, d.Config.StorageR2AccountID, d.Config.StorageR2AccessKeyID, d.Config.StorageR2SecretAccessKey, d.Config.StorageR2Bucket, d.Config.StorageS3Endpoint)
	if err != nil {
		return nil, fmt.Errorf("storage s3 init: %w", err)
	}
	return &Module{Store: store}, nil
}

var _ storage.Store = (*R2Store)(nil)
