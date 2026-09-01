package posthog

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestModuleStopFlushesQueuedEvent(t *testing.T) {
	events := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		events <- string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	m, err := NewModule(context.Background(), nil, Deps{APIKey: "phc_test", Host: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	m.Value.Capture("user_lifecycle", "event_lifecycle", map[string]any{"ok": true})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	select {
	case body := <-events:
		if !strings.Contains(body, "event_lifecycle") || !strings.Contains(body, "user_lifecycle") {
			t.Fatalf("flushed body = %s", body)
		}
	case <-ctx.Done():
		t.Fatal("queued event was not flushed before Stop returned")
	}
}

func TestNewRequiresCredentials(t *testing.T) {
	if _, err := New("", ""); err == nil {
		t.Fatal("missing credentials accepted")
	}
}
