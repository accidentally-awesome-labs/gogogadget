// Package memory implements the local cache target.
package memory

import (
	"context"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/cache"
	"sync"
	"time"
)

type item struct {
	value   []byte
	expires time.Time
}
type Store struct {
	mu    sync.RWMutex
	items map[string]item
}

func New() *Store { return &Store{items: make(map[string]item)} }

var _ cache.Store = (*Store)(nil)

func (s *Store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	s.mu.RLock()
	v, ok := s.items[key]
	s.mu.RUnlock()
	if !ok || (!v.expires.IsZero() && time.Now().After(v.expires)) {
		if ok {
			s.mu.Lock()
			delete(s.items, key)
			s.mu.Unlock()
		}
		return nil, false, nil
	}
	return append([]byte(nil), v.value...), true, nil
}
func (s *Store) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	exp := time.Time{}
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	s.mu.Lock()
	s.items[key] = item{append([]byte(nil), value...), exp}
	s.mu.Unlock()
	return nil
}
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.items, key)
	s.mu.Unlock()
	return nil
}

func (s *Store) Health(ctx context.Context) error { return ctx.Err() }

type Deps struct{}
type Module struct{ Value *Store }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	return &Module{Value: New()}, ctx.Err()
}

func (m *Module) Health(ctx context.Context) error { return ctx.Err() }
