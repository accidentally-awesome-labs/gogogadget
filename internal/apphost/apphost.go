// Package apphost is the leaf environment seam every runtime module depends
// on. It exposes only the ambient services a module needs — configuration
// lookup, logging, clock, and version — so module packages never import the
// generated aggregator that wires them together.
//
// Two implementations exist: OS reads the process environment and wall clock,
// and Map wraps a caller-supplied environment and fixed clock for tests.
package apphost

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// Host is the ambient environment a runtime module is constructed against.
type Host interface {
	// Env returns the named environment value, or "" when unset.
	Env(key string) string
	// Log returns the host logger.
	Log() *slog.Logger
	// Now returns the host clock. Production uses wall time; tests fix it.
	Now() time.Time
	// Version returns the build version stamped into the binary.
	Version() string
}

// Stop is a graceful-shutdown hook. It must be idempotent and must return
// ctx.Err() when the context expires.
type Stop func(context.Context) error

// Lifecycle is the reusable module shutdown contract. Implementations must
// perform shutdown at most once and honor the caller's context deadline.
type Lifecycle interface {
	Stop(context.Context) error
}

// HealthChecker is the reusable provider health contract. A nil error means
// the provider is healthy; implementations must honor context cancellation.
type HealthChecker interface {
	Health(context.Context) error
}

// osHost implements Host against the real process environment.
type osHost struct {
	version string
	log     *slog.Logger
}

// OS returns a Host backed by the process environment and wall clock. The
// logger is built here because every module takes one at construction, and the
// runtime cannot parse configuration before its first module exists — so level
// and format come from the ambient environment, which is what a host is for.
func OS(version string) Host {
	return &osHost{version: version, log: environmentLogger()}
}

// environmentLogger reads LOG_LEVEL and APP_ENV directly. Development defaults
// to debug because that is the loop where you want it; production emits JSON so
// a log pipeline can parse it.
func environmentLogger() *slog.Logger {
	env := os.Getenv("APP_ENV")
	if env == "" {
		env = "development"
	}
	level := slog.LevelInfo
	switch os.Getenv("LOG_LEVEL") {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	case "":
		if env == "development" {
			level = slog.LevelDebug
		}
	}
	opts := &slog.HandlerOptions{Level: level}
	if env == "production" {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}

func (h *osHost) Env(key string) string { return os.Getenv(key) }
func (h *osHost) Log() *slog.Logger     { return h.log }
func (h *osHost) Now() time.Time        { return time.Now() }
func (h *osHost) Version() string       { return h.version }

// mapHost implements Host against a caller-supplied environment and clock.
type mapHost struct {
	env     map[string]string
	at      time.Time
	version string
	log     *slog.Logger
}

// Map returns a Host backed by the supplied environment map and a fixed clock
// at at. The map is copied so later mutation by the caller cannot change the
// host's view.
func Map(env map[string]string, at time.Time, version string) Host {
	copied := make(map[string]string, len(env))
	for k, v := range env {
		copied[k] = v
	}
	return &mapHost{env: copied, at: at, version: version, log: slog.Default()}
}

func (h *mapHost) Env(key string) string { return h.env[key] }
func (h *mapHost) Log() *slog.Logger     { return h.log }
func (h *mapHost) Now() time.Time        { return h.at }
func (h *mapHost) Version() string       { return h.version }

// HealthCheck is the result of one runtime system or provider check.
type HealthCheck struct {
	Module   string `json:"module"`
	Slot     string `json:"slot"`
	Adapter  string `json:"adapter"`
	Target   string `json:"target"`
	Error    string `json:"error"`
	Critical bool   `json:"critical"`
	Healthy  bool   `json:"healthy"`
}
type HealthReport struct {
	CheckedAt time.Time     `json:"checked_at"`
	Checks    []HealthCheck `json:"checks"`
	Ready     bool          `json:"ready"`
}
type HealthRegistration struct {
	Module   string
	Slot     string
	Adapter  string
	Target   string
	Critical bool
	Check    HealthChecker
}

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
			result := make(chan error, 1)
			go func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						result <- fmt.Errorf("health check panicked: %v", recovered)
					}
				}()
				if registration.Check == nil {
					result <- fmt.Errorf("health checker is nil")
					return
				}
				result <- registration.Check.Health(checkCtx)
			}()
			select {
			case err := <-result:
				if err != nil {
					check.Error = err.Error()
				}
			case <-checkCtx.Done():
				check.Error = checkCtx.Err().Error()
			}
			check.Healthy = check.Error == ""
			checks[i] = check
		}(i, registration)
	}
	wg.Wait()
	ready := true
	for _, check := range checks {
		if check.Critical && !check.Healthy {
			ready = false
		}
	}
	return HealthReport{CheckedAt: checked, Checks: checks, Ready: ready}
}

type HealthCache struct {
	mu     sync.Mutex
	report HealthReport
	at     time.Time
}

func (c *HealthCache) Get(ctx context.Context, registrations []HealthRegistration) HealthReport {
	c.mu.Lock()
	if time.Since(c.at) < 10*time.Second && c.report.Checks != nil {
		report := c.report
		c.mu.Unlock()
		return report
	}
	c.mu.Unlock()
	report := AggregateHealth(ctx, registrations)
	c.mu.Lock()
	c.report, c.at = report, time.Now()
	c.mu.Unlock()
	return report
}
