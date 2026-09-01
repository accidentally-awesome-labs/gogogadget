// Package memory implements the local rate-limit target.
package memory

import (
	"context"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"sync"
	"time"

	"github.com/gogogadget/gogogadget/internal/ratelimit"
	"golang.org/x/time/rate"
)

type Limiter struct {
	mu           sync.Mutex
	entries      map[string]*rate.Limiter
	limit, burst int
	rate         rate.Limit
}

func New(perMinute, burst int) *Limiter {
	if perMinute <= 0 {
		perMinute = 100
	}
	if burst <= 0 {
		burst = perMinute * 2
	}
	return &Limiter{entries: make(map[string]*rate.Limiter), limit: perMinute, burst: burst, rate: rate.Every(time.Minute / time.Duration(perMinute))}
}

var _ ratelimit.Limiter = (*Limiter)(nil)

func (l *Limiter) Allow(ctx context.Context, key string) (ratelimit.Decision, error) {
	if err := ctx.Err(); err != nil {
		return ratelimit.Decision{}, err
	}
	l.mu.Lock()
	bucket := l.entries[key]
	if bucket == nil {
		bucket = rate.NewLimiter(l.rate, l.burst)
		l.entries[key] = bucket
	}
	allowed := bucket.Allow()
	remaining := int(bucket.Tokens())
	l.mu.Unlock()
	d := ratelimit.Decision{Allowed: allowed, Limit: l.limit, Remaining: remaining}
	if !allowed {
		d.RetryAfter = time.Second
	}
	return d, nil
}

func (l *Limiter) Health(ctx context.Context) error { return ctx.Err() }

type Deps struct{}
type Module struct{ Value *Limiter }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	return &Module{Value: New(100, 200)}, ctx.Err()
}

func (m *Module) Health(ctx context.Context) error { return ctx.Err() }
