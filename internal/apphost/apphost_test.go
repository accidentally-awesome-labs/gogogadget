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
