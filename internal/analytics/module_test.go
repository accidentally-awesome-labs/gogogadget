package analytics

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
)

// Capture always has a live seam: unconfigured resolves to the no-op capturer
// so every call site can fire and forget without a nil check.
func TestNewModuleFallsBackToNoopCapturer(t *testing.T) {
	h := apphost.Map(nil, time.Now(), "test")

	m, err := NewModule(context.Background(), h, Deps{Config: &config.Config{}})
	if err != nil {
		t.Fatalf("NewModule(unconfigured): %v", err)
	}
	if _, ok := m.Capturer.(NoopCapturer); !ok {
		t.Fatalf("unconfigured capturer = %T, want NoopCapturer", m.Capturer)
	}
}

type blockingCapturer struct {
	started chan struct{}
	release chan struct{}
	closes  atomic.Int32
}

func (c *blockingCapturer) Capture(string, string, map[string]any) {}

func (c *blockingCapturer) Close() {
	c.closes.Add(1)
	close(c.started)
	<-c.release
}

func TestModuleStopIsIdempotentAndContextBounded(t *testing.T) {
	c := &blockingCapturer{started: make(chan struct{}), release: make(chan struct{})}
	m := &Module{Capturer: c, closed: make(chan struct{})}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := m.Stop(ctx); err == nil {
		t.Fatal("Stop should report the caller deadline while capturer is blocked")
	}
	if got := c.closes.Load(); got != 1 {
		t.Fatalf("Close calls after timed-out Stop = %d, want 1", got)
	}

	close(c.release)
	select {
	case <-m.closed:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish after capturer released")
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if got := c.closes.Load(); got != 1 {
		t.Fatalf("Close calls after second Stop = %d, want 1", got)
	}
}

func TestModuleStopFlushesQueuedPostHogEvents(t *testing.T) {
	events := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		events <- body
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status": 1}`))
	}))
	defer srv.Close()

	m, err := NewModule(context.Background(), apphost.Map(nil, time.Now(), "test"), Deps{
		Config: &config.Config{PostHogAPIKey: "phc_contract_key", PostHogHost: srv.URL},
	})
	if err != nil {
		t.Fatalf("NewModule(configured): %v", err)
	}
	m.Capturer.Capture("user_shutdown", "shutdown_event", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	select {
	case body := <-events:
		if got := string(body); !strings.Contains(got, "shutdown_event") || !strings.Contains(got, "user_shutdown") {
			t.Fatalf("flushed event body = %s", got)
		}
	default:
		t.Fatal("queued PostHog event was not delivered before Stop returned")
	}
}

func TestNewModuleRejectsMissingConfig(t *testing.T) {
	h := apphost.Map(nil, time.Now(), "test")
	if _, err := NewModule(context.Background(), h, Deps{}); err == nil {
		t.Fatal("NewModule(nil config) = nil error, want failure")
	}
}
