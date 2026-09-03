package web

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capturingReporter records what the observability seam was handed. The
// assertion target is the seam, never a vendor SDK.
type capturingReporter struct {
	mu       sync.Mutex
	requests []string
	errors   []string
}

func (c *capturingReporter) Capture(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errors = append(c.errors, err.Error())
}

func (c *capturingReporter) CaptureRequest(r *http.Request, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, r.URL.Path+" "+err.Error())
}

func (c *capturingReporter) captured() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string{}, c.requests...)
}

// TestPanicBecomesFiveHundredThroughHandlerChain proves the recover middleware
// is actually installed in Handler(): a panicking handler must render the 500
// page and reach the observability reporter. Without recover in the chain the
// panic escapes ServeHTTP and this test dies on the panic itself.
func TestPanicBecomesFiveHundredThroughHandlerChain(t *testing.T) {
	reporter := &capturingReporter{}
	s := integrationServer(t, func(d *Deps) { d.Reporter = reporter })
	s.mux.HandleFunc("GET /test-only/panic", func(http.ResponseWriter, *http.Request) {
		panic("handler exploded")
	})

	code, _, body := serve(t, s, "GET", "/test-only/panic", nil, nil)

	require.Equal(t, http.StatusInternalServerError, code)
	assert.Contains(t, body, "500")
	// Outside production the page carries the panic detail (cfg.Env is "test").
	assert.Contains(t, body, "handler exploded")
	captured := reporter.captured()
	require.Len(t, captured, 1, "the panic must reach observability.Reporter exactly once")
	assert.Contains(t, captured[0], "/test-only/panic")
	assert.Contains(t, captured[0], "panic: handler exploded")
}

// healthOf builds a runtime.health capability returning the supplied checks.
func healthOf(checks ...apphost.HealthCheck) apphost.HealthFunc {
	ready := true
	for _, check := range checks {
		if check.Critical && !check.Healthy {
			ready = false
		}
	}
	return func(ctx context.Context) apphost.HealthReport {
		return apphost.HealthReport{CheckedAt: time.Now(), Checks: checks, Ready: ready}
	}
}

// TestReadyzConsultsRuntimeHealth proves /readyz is a consumer of the
// generated Runtime.Health report and honours the critical/non-critical split
// the provider slot declarations carry: a critical slot sheds traffic, a
// non-critical one is reported degraded and keeps serving.
func TestReadyzConsultsRuntimeHealth(t *testing.T) {
	for _, tc := range []struct {
		name     string
		checks   []apphost.HealthCheck
		wantCode int
		wantBody readyzResponse
	}{
		{
			name: "critical slot unhealthy sheds traffic",
			checks: []apphost.HealthCheck{
				{Module: "ggg/system/database-postgres", Slot: "ggg/database", Critical: true, Healthy: false, Error: "pool closed"},
				{Module: "ggg/system/mail-dev", Slot: "ggg/mail", Critical: false, Healthy: true},
			},
			wantCode: http.StatusServiceUnavailable,
			wantBody: readyzResponse{Status: "critical slot unhealthy", Critical: []string{"ggg/database"}},
		},
		{
			name: "non-critical slot unhealthy still serves",
			checks: []apphost.HealthCheck{
				{Module: "ggg/system/database-postgres", Slot: "ggg/database", Critical: true, Healthy: true},
				{Module: "ggg/system/mail-resend", Slot: "ggg/mail", Critical: false, Healthy: false, Error: "429 from provider"},
			},
			wantCode: http.StatusOK,
			wantBody: readyzResponse{Status: "degraded", Degraded: []string{"ggg/mail"}},
		},
		{
			name: "every slot healthy",
			checks: []apphost.HealthCheck{
				{Module: "ggg/system/database-postgres", Slot: "ggg/database", Critical: true, Healthy: true},
				{Module: "ggg/system/mail-dev", Slot: "ggg/mail", Critical: false, Healthy: true},
			},
			wantCode: http.StatusOK,
			wantBody: readyzResponse{Status: "ok"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := integrationServer(t, func(d *Deps) { d.Health = healthOf(tc.checks...) })

			code, _, body := serve(t, s, "GET", "/readyz", nil, nil)

			require.Equal(t, tc.wantCode, code)
			var got readyzResponse
			require.NoError(t, json.Unmarshal([]byte(body), &got))
			assert.Equal(t, tc.wantBody, got)
			// A probe body names slots, never check messages.
			assert.NotContains(t, body, "pool closed")
			assert.NotContains(t, body, "429 from provider")
		})
	}
}

// TestReadyzDegradesWithoutHealthCapability keeps a Server built directly by a
// test (no generated runtime) on the database ping alone.
func TestReadyzDegradesWithoutHealthCapability(t *testing.T) {
	s := integrationServer(t, nil)
	require.Nil(t, s.health)

	code, _, body := serve(t, s, "GET", "/readyz", nil, nil)

	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `"status":"ok"`)
}
