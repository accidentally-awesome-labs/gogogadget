package web

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/realtime"
)

type flushRecorder struct {
	header  http.Header
	mu      sync.Mutex
	body    bytes.Buffer
	status  int
	flushed chan struct{}
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{header: make(http.Header), flushed: make(chan struct{}, 1)}
}

func (r *flushRecorder) Header() http.Header { return r.header }

func (r *flushRecorder) WriteHeader(status int) {
	r.mu.Lock()
	r.status = status
	r.mu.Unlock()
}

func (r *flushRecorder) Write(payload []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.Write(payload)
}

func (r *flushRecorder) Flush() {
	select {
	case r.flushed <- struct{}{}:
	default:
	}
}

func (r *flushRecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.String()
}

func TestServeRealtimePublishesSSEAndClosesOnCancel(t *testing.T) {
	broker := realtime.NewMemory()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/events/org-1", nil).WithContext(ctx)
	rec := newFlushRecorder()
	s := &Server{realtime: broker}
	done := make(chan struct{})
	go func() {
		s.serveRealtime(rec, req)
		close(done)
	}()

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		if err := broker.Publish(context.Background(), "org-1", []byte(`{"kind":"changed"}`)); err != nil {
			t.Fatal(err)
		}
		select {
		case <-rec.flushed:
			goto delivered
		case <-done:
			t.Fatal("SSE handler closed before delivering the event")
		case <-deadline.C:
			t.Fatal("SSE event not delivered")
		case <-time.After(time.Millisecond):
		}
	}

delivered:
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not close after cancellation")
	}
	body := rec.String()
	if !strings.Contains(body, "data:") || !strings.Contains(body, "changed") {
		t.Fatalf("body = %q", body)
	}
}
