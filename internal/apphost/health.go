package apphost

import (
	"context"
	"sync"
	"time"
)

// HealthCheck is the result of one runtime system or provider check.
type HealthCheck struct {
	Module string `json:"module"`
	Slot string `json:"slot"`
	Adapter string `json:"adapter"`
	Target string `json:"target"`
	Error string `json:"error"`
	Critical bool `json:"critical"`
	Healthy bool `json:"healthy"`
}

// HealthReport is the cached runtime readiness report.
type HealthReport struct {
	CheckedAt time.Time `json:"checked_at"`
	Checks []HealthCheck `json:"checks"`
	Ready bool `json:"ready"`
}

// HealthRegistration describes a selected constructor health hook.
type HealthRegistration struct {
	Module string
	Slot string
	Adapter string
	Target string
	Critical bool
	Check HealthChecker
}

// AggregateHealth runs checks concurrently with a two-second budget. Panics
// become unhealthy checks and only critical failures affect readiness.
func AggregateHealth(ctx context.Context, registrations []HealthRegistration) HealthReport {
	checked := time.Now()
	checks := make([]HealthCheck, len(registrations))
	var wg sync.WaitGroup
	for i, registration := range registrations {
		wg.Add(1)
		go func(i int, registration HealthRegistration) {
			defer wg.Done()
			check := HealthCheck{Module: registration.Module, Slot: registration.Slot, Adapter: registration.Adapter, Target: registration.Target, Critical: registration.Critical}
			checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			func() {
				defer func() { if recovered := recover(); recovered != nil { check.Error = "health check panicked" } }()
				if registration.Check == nil { check.Error = "health checker is nil"; return }
				if err := registration.Check.Health(checkCtx); err != nil { check.Error = err.Error() }
			}()
			check.Healthy = check.Error == ""
			checks[i] = check
		}(i, registration)
	}
	wg.Wait()
	ready := true
	for _, check := range checks { if check.Critical && !check.Healthy { ready = false } }
	return HealthReport{CheckedAt: checked, Checks: checks, Ready: ready}
}

// HealthCache caches an aggregate report for ten seconds.
type HealthCache struct { mu sync.Mutex; report HealthReport; at time.Time }
func (c *HealthCache) Get(ctx context.Context, registrations []HealthRegistration) HealthReport {
	c.mu.Lock(); defer c.mu.Unlock()
	if time.Since(c.at) < 10*time.Second && c.report.Checks != nil { return c.report }
	c.report = AggregateHealth(ctx, registrations); c.at = time.Now(); return c.report
}
