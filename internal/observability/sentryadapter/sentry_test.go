package sentryadapter

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestModuleOwnedReporterFlushesOnStop(t *testing.T) {
	bodies := make(chan string, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies <- string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	dsn := "http://public@" + strings.TrimPrefix(srv.URL, "http://") + "/1"
	m, err := NewModule(context.Background(), nil, Deps{DSN: dsn, Environment: "test"})
	if err != nil {
		t.Fatal(err)
	}
	m.Value.Capture(assertErr("sentry lifecycle"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case body := <-bodies:
		if !strings.Contains(body, "sentry lifecycle") {
			t.Fatalf("sentry payload = %s", body)
		}
	case <-ctx.Done():
		t.Fatal("Sentry event not flushed before Stop")
	}
}
func assertErr(s string) error { return lifecycleError(s) }

type lifecycleError string

func (e lifecycleError) Error() string { return string(e) }
func TestNewRequiresDSN(t *testing.T) {
	if _, err := NewModule(context.Background(), nil, Deps{}); err == nil {
		t.Fatal("missing dsn accepted")
	}
}
