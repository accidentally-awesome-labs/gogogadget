package ratelimit

import (
	"context"
	"time"

	"golang.org/x/time/rate"
)

// MockLimiter is the rate-limit seam's own Limiter double. It spends from the
// seam's keyed token bucket — the same Keyed the JSON transport uses
// directly — so a payload gets real per-key budget behaviour without naming
// an adapter package: an adapter is a per-environment provider selection,
// and a test payload that constructs one compiles only while that selection
// holds.
type MockLimiter struct {
	// Err, when non-nil, is returned by every decision. A limiter whose
	// backing service is unavailable must fail closed, so this is how a
	// payload drives that path.
	Err error

	keyed *Keyed
	limit int
}

// NewMockLimiter budgets perMinute requests per key, bursting to burst.
func NewMockLimiter(perMinute, burst int) *MockLimiter {
	if perMinute <= 0 {
		perMinute = 100
	}
	if burst <= 0 {
		burst = perMinute * 2
	}
	return &MockLimiter{keyed: NewKeyed(rate.Every(time.Minute/time.Duration(perMinute)), burst), limit: perMinute}
}

func (m *MockLimiter) Allow(ctx context.Context, key string) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	if m.Err != nil {
		return Decision{}, m.Err
	}
	allowed := m.keyed.Allow(key)
	d := Decision{Allowed: allowed, Limit: m.limit}
	if !allowed {
		d.RetryAfter = time.Second
	}
	return d, nil
}

var _ Limiter = (*MockLimiter)(nil)
