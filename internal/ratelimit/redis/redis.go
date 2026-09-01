// Package redis implements the shared rate-limit target without coupling the
// seam to a particular Redis client. Valkey and Upstash clients adapt Client.
package redis

import (
	"context"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"time"

	"github.com/gogogadget/gogogadget/internal/ratelimit"
)

type Client interface {
	Allow(context.Context, string, int, time.Duration) (remaining int, allowed bool, err error)
}
type Limiter struct {
	client Client
	limit  int
	window time.Duration
}

func New(client Client, perMinute int) (*Limiter, error) {
	if client == nil {
		return nil, fmt.Errorf("redis rate limit: client is required")
	}
	if perMinute <= 0 {
		perMinute = 100
	}
	return &Limiter{client: client, limit: perMinute, window: time.Minute}, nil
}

var _ ratelimit.Limiter = (*Limiter)(nil)

func (l *Limiter) Allow(ctx context.Context, key string) (ratelimit.Decision, error) {
	remaining, allowed, err := l.client.Allow(ctx, key, l.limit, l.window)
	if err != nil {
		return ratelimit.Decision{Allowed: false, Limit: l.limit}, fmt.Errorf("rate limit backend: %w", err)
	}
	d := ratelimit.Decision{Allowed: allowed, Limit: l.limit, Remaining: remaining}
	if !allowed {
		d.RetryAfter = time.Second
	}
	return d, nil
}

func (l *Limiter) Health(ctx context.Context) error {
	if l == nil || l.client == nil {
		return fmt.Errorf("rate limit backend is unavailable")
	}
	return nil
}

type Deps struct{ Client Client }
type Module struct{ Value *Limiter }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if d.Client == nil {
		d.Client = unavailableClient{}
	}
	v, err := New(d.Client, 100)
	if err != nil {
		return nil, err
	}
	return &Module{Value: v}, ctx.Err()
}

type unavailableClient struct{}

func (unavailableClient) Allow(context.Context, string, int, time.Duration) (int, bool, error) {
	return 0, false, fmt.Errorf("redis rate limit: REDIS_URL is not configured")
}

func (m *Module) Health(ctx context.Context) error {
	if m == nil || m.Value == nil {
		return fmt.Errorf("rate limit redis: limiter is required")
	}
	return m.Value.Health(ctx)
}
