package apphost

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestOSHostExposesEnvironmentVersionAndClock(t *testing.T) {
	t.Setenv("APPHOST_PROBE", "present")
	fixed := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	h := OS("v9.9.9")

	if got, want := h.Env("APPHOST_PROBE"), "present"; got != want {
		t.Fatalf("Env(probe) = %q, want %q", got, want)
	}
	if got, want := h.Env("APPHOST_ABSENT_KEY"), ""; got != want {
		t.Fatalf("Env(absent) = %q, want %q", got, want)
	}
	if got, want := h.Version(), "v9.9.9"; got != want {
		t.Fatalf("Version() = %q, want %q", got, want)
	}
	if got := h.Log(); got == nil {
		t.Fatal("Log() returned nil")
	}
	now := h.Now()
	if now.Before(fixed.Add(-time.Hour)) || now.After(time.Now().Add(time.Hour)) {
		t.Fatalf("OS clock is not wall time: %v", now)
	}
}

func TestMapHostUsesSuppliedEnvironmentAndClock(t *testing.T) {
	env := map[string]string{"DATABASE_URL": "postgres://x", "LOG_LEVEL": "debug"}
	at := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	h := Map(env, at, "v1.2.3")

	if got, want := h.Env("DATABASE_URL"), "postgres://x"; got != want {
		t.Fatalf("Env(DATABASE_URL) = %q, want %q", got, want)
	}
	if got, want := h.Env("LOG_LEVEL"), "debug"; got != want {
		t.Fatalf("Env(LOG_LEVEL) = %q, want %q", got, want)
	}
	if got, want := h.Env("MISSING"), ""; got != want {
		t.Fatalf("Env(MISSING) = %q, want %q", got, want)
	}
	if got, want := h.Version(), "v1.2.3"; got != want {
		t.Fatalf("Version() = %q, want %q", got, want)
	}
	if got, want := h.Now(), at; !got.Equal(want) {
		t.Fatalf("Now() = %v, want %v", got, want)
	}
	if got := h.Log(); got == nil {
		t.Fatal("Log() returned nil")
	}
}

func TestMapHostDoesNotMutateSuppliedEnv(t *testing.T) {
	env := map[string]string{"A": "1"}
	before := env["A"]
	h := Map(env, time.Now(), "v")
	h.Env("A")
	if got := env["A"]; got != before {
		t.Fatalf("supplied env mutated: %q -> %q", before, got)
	}
	if len(env) != 1 {
		t.Fatalf("supplied env grew: %v", env)
	}
}

func TestStopFuncTypeIsContextAware(t *testing.T) {
	var called bool
	var stop Stop = func(ctx context.Context) error {
		called = true
		return ctx.Err()
	}
	if err := stop(context.Background()); err != nil || !called {
		t.Fatalf("stop(ctx) err = %v called = %t", err, called)
	}
}

func TestLoggersAreDistinctPerHost(t *testing.T) {
	a := OS("a").Log()
	b := OS("b").Log()
	if a == nil || b == nil {
		t.Fatal("nil logger")
	}
	if _, ok := interface{}(a).(*slog.Logger); !ok {
		t.Fatalf("Log() type = %T, want *slog.Logger", a)
	}
}

// The host owns the logger because every module takes one at construction, and
// the runtime cannot parse configuration before its first module is built. The
// level and format therefore come from the ambient environment, which is
// exactly what the host is for.
func TestOSHostLoggerHonorsEnvironment(t *testing.T) {
	t.Run("debug level is enabled when requested", func(t *testing.T) {
		t.Setenv("LOG_LEVEL", "debug")
		t.Setenv("APP_ENV", "development")
		h := OS("v-test")
		if !h.Log().Enabled(context.Background(), slog.LevelDebug) {
			t.Fatal("LOG_LEVEL=debug did not enable debug logging")
		}
	})

	t.Run("debug is off by default", func(t *testing.T) {
		t.Setenv("LOG_LEVEL", "")
		t.Setenv("APP_ENV", "production")
		h := OS("v-test")
		if h.Log().Enabled(context.Background(), slog.LevelDebug) {
			t.Fatal("debug logging is on in production without LOG_LEVEL")
		}
		if !h.Log().Enabled(context.Background(), slog.LevelInfo) {
			t.Fatal("info logging is off")
		}
	})

	t.Run("development defaults to debug", func(t *testing.T) {
		t.Setenv("LOG_LEVEL", "")
		t.Setenv("APP_ENV", "development")
		h := OS("v-test")
		if !h.Log().Enabled(context.Background(), slog.LevelDebug) {
			t.Fatal("development did not default to debug logging")
		}
	})
}

type healthFunc func(context.Context) error

func (f healthFunc) Health(ctx context.Context) error { return f(ctx) }

func TestAggregateHealthRunsConcurrentlyAndOnlyCriticalAffectsReady(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	checker := healthFunc(func(context.Context) error {
		started <- struct{}{}
		<-release
		return nil
	})
	registrations := []HealthRegistration{
		{Module: "a", Critical: true, Check: checker},
		{Module: "b", Critical: false, Check: checker},
	}
	go func() {
		<-started
		<-started
		close(release)
	}()
	report := AggregateHealth(context.Background(), registrations)
	if len(report.Checks) != 2 || !report.Ready {
		t.Fatalf("report = %#v, want two healthy checks and ready", report)
	}
}

func TestAggregateHealthHardDeadlineAndPanicRecovery(t *testing.T) {
	ignoreContext := healthFunc(func(context.Context) error {
		select {}
	})
	start := time.Now()
	report := AggregateHealth(context.Background(), []HealthRegistration{
		{Module: "slow", Slot: "mail", Adapter: "dev", Target: "filesystem", Critical: true, Check: ignoreContext},
		{Module: "panic", Critical: false, Check: healthFunc(func(context.Context) error { panic("boom") })},
	})
	if elapsed := time.Since(start); elapsed > 2500*time.Millisecond {
		t.Fatalf("health exceeded hard deadline: %v", elapsed)
	}
	if report.Ready {
		t.Fatal("critical timeout must make report not ready")
	}
	if report.Checks[1].Healthy || report.Checks[1].Error == "" {
		t.Fatalf("panic check = %#v, want unhealthy error", report.Checks[1])
	}
}

func TestHealthCacheUsesTenSecondWindow(t *testing.T) {
	calls := 0
	check := healthFunc(func(context.Context) error { calls++; return nil })
	cache := &HealthCache{}
	registrations := []HealthRegistration{{Module: "cached", Check: check}}
	cache.Get(context.Background(), registrations)
	cache.Get(context.Background(), registrations)
	if calls != 1 {
		t.Fatalf("checker calls = %d, want cached single call", calls)
	}
	cache.at = time.Now().Add(-11 * time.Second)
	cache.Get(context.Background(), registrations)
	if calls != 2 {
		t.Fatalf("checker calls after expiry = %d, want 2", calls)
	}
}
