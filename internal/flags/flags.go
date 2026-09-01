// Package flags is the feature-flag seam: DB-backed flags with per-org
// overrides, evaluated deterministically. Admins manage flags at /admin/flags.
package flags

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/gogogadget/gogogadget/internal/cache"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
)

// Evaluator answers "is this feature on for this org".
type Evaluator interface {
	Enabled(ctx context.Context, orgID, key string) bool
}

// DBEvaluator evaluates against the database with a 30s in-process cache of
// the flag rows (overrides are read per call — they are PK lookups).
type DBEvaluator struct {
	q     *sqlc.Queries
	ttl   time.Duration
	cache cache.Store
	// Report receives cache/database failures that cannot be returned through
	// the historical bool Evaluator API. Callers may wire observability here.
	Report func(context.Context, error)

	mu      sync.Mutex
	cached  map[string]sqlc.FeatureFlag
	expires time.Time
}

func NewDBEvaluator(q *sqlc.Queries, ttl time.Duration) *DBEvaluator {
	return &DBEvaluator{q: q, ttl: ttl}
}
func NewDBEvaluatorWithCache(q *sqlc.Queries, ttl time.Duration, c cache.Store) *DBEvaluator {
	return &DBEvaluator{q: q, ttl: ttl, cache: c}
}

// Invalidate drops the flag-row cache so the next evaluation re-reads the
// table. Admin mutations call it: without it, a created flag stays missing
// and a deleted flag stays ON for up to the TTL.
func (e *DBEvaluator) Invalidate() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cached = nil
	e.expires = time.Time{}
}

func (e *DBEvaluator) flags(ctx context.Context) map[string]sqlc.FeatureFlag {
	e.mu.Lock()
	defer e.mu.Unlock()
	if time.Now().Before(e.expires) && e.cached != nil {
		return e.cached
	}
	if e.cache != nil {
		if raw, ok, err := e.cache.Get(ctx, "flags:all"); err == nil && ok {
			var rows []sqlc.FeatureFlag
			if json.Unmarshal(raw, &rows) == nil {
				m := make(map[string]sqlc.FeatureFlag, len(rows))
				for _, r := range rows {
					m[r.Key] = r
				}
				e.cached, e.expires = m, time.Now().Add(e.ttl)
				return m
			}
		} else if err != nil && e.Report != nil {
			e.Report(ctx, fmt.Errorf("flags cache read: %w", err))
		}
	}
	rows, err := e.q.ListFeatureFlags(ctx)
	if err != nil {
		if e.Report != nil {
			e.Report(ctx, fmt.Errorf("flags database read: %w", err))
		}
		if e.cached == nil {
			e.cached = map[string]sqlc.FeatureFlag{}
		}
		return e.cached
	}
	m := make(map[string]sqlc.FeatureFlag, len(rows))
	for _, r := range rows {
		m[r.Key] = r
	}
	e.cached = m
	e.expires = time.Now().Add(e.ttl)
	if e.cache != nil {
		if raw, marshalErr := json.Marshal(rows); marshalErr == nil {
			_ = e.cache.Set(ctx, "flags:all", raw, e.ttl)
		}
	}
	return m
}

// Enabled semantics (fixed): missing key → false; a per-org override wins
// over everything; else enabled && (rollout == 100 || bucket < rollout).
// Bucketing is FNV over "org|key" — deterministic per org, stable across
// rollout increases (50% is a subset of 60%).
func (e *DBEvaluator) Enabled(ctx context.Context, orgID, key string) bool {
	if e == nil || e.q == nil {
		if e != nil && e.Report != nil {
			e.Report(ctx, fmt.Errorf("flags database is unavailable"))
		}
		return false
	}
	// Per-org override wins.
	ov, err := e.q.GetFlagOverride(ctx, sqlc.GetFlagOverrideParams{FlagKey: key, OrgID: orgID})
	if err == nil {
		return ov.Enabled
	}
	if e.Report != nil {
		e.Report(ctx, fmt.Errorf("flags override read: %w", err))
	}
	f, ok := e.flags(ctx)[key]
	if !ok || !f.Enabled {
		return false
	}
	if f.Rollout >= 100 {
		return true
	}
	if f.Rollout <= 0 {
		return false
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(orgID + "|" + key))
	return int(h.Sum32()%100) < int(f.Rollout)
}

// All returns the admin table view (live read, cache-free).
func (e *DBEvaluator) All(ctx context.Context) ([]sqlc.FeatureFlag, error) {
	return e.q.ListFeatureFlags(ctx)
}
