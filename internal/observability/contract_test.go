package observability

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
)

// Compile-time conformance for every Reporter implementation.
var (
	_ Reporter = NoopReporter{}
	_ Reporter = (*SentryReporter)(nil)
)

// runReporterContract is the Reporter seam contract: every implementation
// must satisfy it. Reporters are fire-and-forget — the methods must never
// panic and never surface a failure to the caller.
func runReporterContract(t *testing.T, factory func(t *testing.T) Reporter) {
	t.Helper()

	t.Run("CaptureNeverPanics", func(t *testing.T) {
		r := factory(t)
		r.Capture(errors.New("contract-capture-boom"))
	})

	t.Run("CaptureRequestNeverPanics", func(t *testing.T) {
		r := factory(t)
		req := httptest.NewRequest(http.MethodGet, "/contract/path?q=1", nil)
		r.CaptureRequest(req, errors.New("contract-request-boom"))
	})
}

func TestNoopReporterContract(t *testing.T) {
	runReporterContract(t, func(t *testing.T) Reporter { return NoopReporter{} })
}

// TestSentryReporterContract runs the contract against the real
// SentryReporter. SentryReporter reports through the global sentry hub
// (cmd/server owns sentry.Init in production), so the test initializes the
// hub with a DSN pointing at an httptest server and asserts the event
// envelope actually arrives.
func TestSentryReporterContract(t *testing.T) {
	var mu sync.Mutex
	var paths, bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read envelope body: %v", err)
		}
		mu.Lock()
		paths = append(paths, r.URL.Path)
		bodies = append(bodies, string(b))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dsn := "http://public@" + strings.TrimPrefix(srv.URL, "http://") + "/1"
	if err := sentry.Init(sentry.ClientOptions{Dsn: dsn, Environment: "test"}); err != nil {
		t.Fatalf("sentry.Init: %v", err)
	}
	t.Cleanup(func() { sentry.CurrentHub().BindClient(nil) })

	runReporterContract(t, func(t *testing.T) Reporter { return NewSentryReporter() })

	if !sentry.Flush(5*time.Second) {
		t.Fatal("sentry.Flush timed out waiting for events")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) == 0 {
		t.Fatal("no sentry envelope arrived at the test server")
	}
	for _, p := range paths {
		if p != "/api/1/envelope/" {
			t.Fatalf("unexpected sentry endpoint %q, want /api/1/envelope/", p)
		}
	}
	joined := strings.Join(bodies, "\n")
	for _, want := range []string{"contract-capture-boom", "contract-request-boom", "/contract/path"} {
		if !strings.Contains(joined, want) {
			t.Errorf("envelope payload missing %q\n--- envelopes ---\n%s", want, joined)
		}
	}
}
