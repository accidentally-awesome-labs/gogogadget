// Package redis adapts a Redis-compatible cache client. The interface keeps
// the provider seam free of Valkey/Upstash SDKs.
package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/cache"
)

type Client interface {
	Get(context.Context, string) ([]byte, bool, error)
	Set(context.Context, string, []byte, time.Duration) error
	Delete(context.Context, string) error
}
type Store struct{ client Client }

func New(client Client) (*Store, error) {
	if client == nil {
		return nil, fmt.Errorf("redis cache: client is required")
	}
	return &Store{client: client}, nil
}

var _ cache.Store = (*Store)(nil)

func (s *Store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if s.client == nil {
		return nil, false, context.Canceled
	}
	return s.client.Get(ctx, key)
}
func (s *Store) Set(ctx context.Context, key string, v []byte, ttl time.Duration) error {
	if s.client == nil {
		return context.Canceled
	}
	return s.client.Set(ctx, key, v, ttl)
}
func (s *Store) Delete(ctx context.Context, key string) error {
	if s.client == nil {
		return context.Canceled
	}
	return s.client.Delete(ctx, key)
}

func (s *Store) Health(ctx context.Context) error {
	if s == nil || s.client == nil {
		return context.Canceled
	}
	return nil
}

type Deps struct{ Client Client }
type Module struct{ Value *Store }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	v, err := New(d.Client)
	if err != nil {
		return nil, err
	}
	return &Module{Value: v}, nil
}

func (m *Module) Health(ctx context.Context) error { return m.Value.Health(ctx) }
