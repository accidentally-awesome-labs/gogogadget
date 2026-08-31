package analytics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Compile-time conformance for every Capturer implementation and the
// buffering lifecycle contract.
var (
	_ Capturer          = NoopCapturer{}
	_ Capturer          = (*PostHogCapturer)(nil)
	_ BufferingCapturer = (*PostHogCapturer)(nil)
)

// runCapturerContract is the Capturer seam contract: Capture never panics,
// never blocks, and (where the impl flushes) the event reaches the provider
// with distinct id, event name, and properties intact.
func runCapturerContract(t *testing.T, factory func(t *testing.T) Capturer, flush func(t *testing.T, c Capturer), assertDelivered func(t *testing.T)) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c := factory(t)
		c.Capture("user_contract", "contract_event", map[string]any{"k": "v"})
		if flush != nil {
			flush(t, c)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Capture/flush must not block")
	}
	if assertDelivered != nil {
		assertDelivered(t)
	}
}

func TestNoopCapturerContract(t *testing.T) {
	runCapturerContract(t,
		func(t *testing.T) Capturer { return NoopCapturer{} },
		nil, // nothing to flush
		nil) // nothing delivered — the point of the no-op
}

func TestPostHogCapturerContract(t *testing.T) {
	events := make(chan []byte, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		events <- raw
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status": 1}`))
	}))
	t.Cleanup(srv.Close)

	c, err := NewPostHog("phc_contract_key", srv.URL)
	require.NoError(t, err)

	runCapturerContract(t,
		func(t *testing.T) Capturer { return c },
		func(t *testing.T, c Capturer) { c.(*PostHogCapturer).Close() }, // Close flushes the queue
		func(t *testing.T) {
			// posthog-go batches; Close() flushes whatever was queued. Accept
			// any batch shape but require OUR event to appear in the payload.
			select {
			case raw := <-events:
				body := string(raw)
				assert.Contains(t, body, "contract_event", "event name must reach the provider")
				assert.Contains(t, body, "user_contract", "distinct id must reach the provider")
			case <-time.After(2 * time.Second):
				t.Fatal("flushed batch never arrived at the endpoint")
			}
		})
}
