// Package ratelimit is the in-process token-bucket limiter shared by both
// transports: the HTML app limits per client IP, the JSON API limits per API
// token. One implementation, two keys — a second copy of a concurrent map
// with a sweeper is exactly the kind of duplication that drifts.
//
// Single-node by design, like everything else in this boilerplate that holds
// state in memory. When scaling horizontally (fly scale count > 1) each
// instance enforces its own budget, so the effective limit multiplies by the
// instance count; that is the documented trigger for swapping in a shared
// store (e.g. Upstash). See /docs/security.
package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type entry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// Keyed holds one token bucket per key, sweeping keys that go quiet so the
// map cannot grow without bound.
type Keyed struct {
	mu      sync.Mutex
	entries map[string]*entry
	rate    rate.Limit
	burst   int
}

// NewKeyed starts a limiter with the given refill rate and burst. The
// returned value owns a background sweeper for its lifetime; callers are
// expected to build one per process, not per request.
func NewKeyed(r rate.Limit, burst int) *Keyed {
	kl := &Keyed{entries: make(map[string]*entry), rate: r, burst: burst}
	go kl.janitor()
	return kl
}

// PerMinute is the common construction: n requests per minute, bursting to
// 2n so a legitimate client that batches is not punished for the shape of
// its traffic.
func PerMinute(n int) *Keyed {
	return NewKeyed(rate.Every(time.Minute/time.Duration(n)), n*2)
}

// Allow reports whether the key may spend a token now.
func (kl *Keyed) Allow(key string) bool {
	kl.mu.Lock()
	e, ok := kl.entries[key]
	if !ok {
		e = &entry{limiter: rate.NewLimiter(kl.rate, kl.burst)}
		kl.entries[key] = e
	}
	e.lastSeen = time.Now()
	kl.mu.Unlock()
	return e.limiter.Allow()
}

// janitor sweeps entries idle for >10 minutes.
func (kl *Keyed) janitor() {
	tick := time.NewTicker(5 * time.Minute)
	defer tick.Stop()
	for range tick.C {
		cutoff := time.Now().Add(-10 * time.Minute)
		kl.mu.Lock()
		for key, e := range kl.entries {
			if e.lastSeen.Before(cutoff) {
				delete(kl.entries, key)
			}
		}
		kl.mu.Unlock()
	}
}
