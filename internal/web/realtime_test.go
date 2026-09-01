package web

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/realtime"
)

func TestServeRealtimePublishesSSEAndClosesOnCancel(t *testing.T) {
	broker := realtime.NewMemory()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/events/org-1", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	s := &Server{realtime: broker}
	done := make(chan struct{})
	go func() { s.serveRealtime(rec, req); close(done) }()
	deadline := time.After(time.Second)
	for {
		if err := broker.Publish(context.Background(), "org-1", []byte(`{"kind":"changed"}`)); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
			break
		default:
		}
		if strings.Contains(rec.Body.String(), "changed") {
			break
		}
		select {
		case <-deadline:
			t.Fatal("SSE event not delivered")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not close after cancellation")
	}
	if !strings.Contains(rec.Body.String(), "data:") {
		t.Fatalf("body = %q", rec.Body.String())
	}
}
