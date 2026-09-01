package ratelimit

import (
	"context"
	"time"
)

// Decision describes the result of spending one request from a keyed budget.
type Decision struct {
	Allowed    bool
	Limit      int
	Remaining  int
	RetryAfter time.Duration
}

// Limiter is the provider-neutral rate-limit capability. Implementations must
// fail closed when their backing service is unavailable.
type Limiter interface {
	Allow(context.Context, string) (Decision, error)
}
