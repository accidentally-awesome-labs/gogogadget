// Package cache is the provider-neutral cache capability. Keys are fully
// owned by callers; adapters never add or remove namespaces.
package cache

import (
	"context"
	"time"
)

type Store interface {
	Get(context.Context, string) ([]byte, bool, error)
	Set(context.Context, string, []byte, time.Duration) error
	Delete(context.Context, string) error
}
