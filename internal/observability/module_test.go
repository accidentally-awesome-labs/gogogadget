package observability

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
)

// Reporting always has a live seam: unconfigured resolves to the no-op so
// callers never nil-check, and a DSN swaps in Sentry.
func TestNewModuleFallsBackToNoopReporter(t *testing.T) {
	h := apphost.Map(nil, time.Now(), "test")

	m, err := NewModule(context.Background(), h, Deps{Config: &config.Config{}})
	if err != nil {
		t.Fatalf("NewModule(unconfigured): %v", err)
	}
	if _, ok := m.Reporter.(NoopReporter); !ok {
		t.Fatalf("unconfigured reporter = %T, want NoopReporter", m.Reporter)
	}
}

func TestConfiguredReporterUsesModuleOwnedClient(t *testing.T) {
	moduleEvents := make(chan struct{}, 1)
	moduleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		moduleEvents <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))

	h := apphost.Map(nil, time.Now(), "test")
	dsn := "http://public@" + strings.TrimPrefix(moduleServer.URL, "http://") + "/1"
	m, err := NewModule(context.Background(), h, Deps{Config: &config.Config{SentryDSN: dsn}})
	if err != nil {
		t.Fatalf("NewModule(configured): %v", err)
	}
	m.Reporter.Capture(errors.New("module-owned-event"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	select {
	case <-moduleEvents:
	default:
		t.Fatal("module-owned Sentry client did not receive event before Stop returned")
	}
}

type blockingTransport struct {
	started chan struct{}
	release chan struct{}
	flushes atomic.Int32
	closes  atomic.Int32
}

func (t *blockingTransport) Configure(sentry.ClientOptions) {}
func (t *blockingTransport) Flush(time.Duration) bool       { return true }
func (t *blockingTransport) SendEvent(*sentry.Event)        {}
func (t *blockingTransport) FlushWithContext(ctx context.Context) bool {
	t.flushes.Add(1)
	close(t.started)
	select {
	case <-t.release:
		return true
	case <-ctx.Done():
		return false
	}
}
func (t *blockingTransport) Close() { t.closes.Add(1) }

func TestModuleStopIsIdempotentAndContextBounded(t *testing.T) {
	transport := &blockingTransport{started: make(chan struct{}), release: make(chan struct{})}
	client, err := sentry.NewClient(sentry.ClientOptions{Transport: transport})
	if err != nil {
		t.Fatalf("sentry.NewClient: %v", err)
	}
	m := &Module{client: client, stopped: make(chan struct{})}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := m.Stop(ctx); err == nil {
		t.Fatal("Stop should report the caller deadline while flush is blocked")
	}
	if got := transport.flushes.Load(); got != 1 {
		t.Fatalf("flush calls after timed-out Stop = %d, want 1", got)
	}

	close(transport.release)
	select {
	case <-m.stopped:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish after transport released")
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop should retry successfully: %v", err)
	}
	if got := transport.flushes.Load(); got != 1 {
		t.Fatalf("flush calls after second Stop = %d, want 1", got)
	}
	if got := transport.closes.Load(); got != 1 {
		t.Fatalf("close calls after second Stop = %d, want 1", got)
	}
}

func TestNewModuleRejectsMissingConfig(t *testing.T) {
	h := apphost.Map(nil, time.Now(), "test")
	if _, err := NewModule(context.Background(), h, Deps{}); err == nil {
		t.Fatal("NewModule(nil config) = nil error, want failure")
	}
}
