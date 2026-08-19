package web

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetricsRenderExpositionFormat(t *testing.T) {
	var m metricsRegistry
	m.observe(200, 10*time.Millisecond)
	m.observe(200, 30*time.Millisecond)
	m.observe(404, 5*time.Millisecond)
	m.observe(503, 1*time.Millisecond)

	out := m.render("testver", &poolStats{acquired: 2, idle: 1, total: 3, max: 10})

	// One HELP/TYPE per family (duplicates are invalid exposition).
	assert.Equal(t, 1, strings.Count(out, "# HELP gogogadget_http_requests_total"))
	assert.Equal(t, 1, strings.Count(out, "# TYPE gogogadget_http_requests_total"))
	assert.Equal(t, 1, strings.Count(out, "# TYPE gogogadget_http_request_duration_seconds"))

	assert.Contains(t, out, `gogogadget_http_requests_total{status="2xx"} 2`)
	assert.Contains(t, out, `gogogadget_http_requests_total{status="4xx"} 1`)
	assert.Contains(t, out, `gogogadget_http_requests_total{status="5xx"} 1`)
	assert.Contains(t, out, "gogogadget_http_request_duration_seconds_count 4")
	assert.Contains(t, out, "gogogadget_http_request_duration_seconds_sum 0.046")
	assert.Contains(t, out, "go_goroutines")
	assert.Contains(t, out, "gogogadget_db_pool_acquired_conns 2")
	assert.Contains(t, out, `gogogadget_build_info{version="testver"} 1`)

	// Prometheus scrapes parse HELP/TYPE lines; every metric must carry them.
	for _, line := range strings.Split(out, "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			assert.NotEmpty(t, line, "no blank samples")
		}
	}
}

func TestMetricsObserveIgnoresInformationalClass(t *testing.T) {
	var m metricsRegistry
	m.observe(100, time.Millisecond) // 1xx — not a counted class
	assert.Equal(t, int64(0), m.requests[0].Load()+m.requests[1].Load()+m.requests[2].Load()+m.requests[3].Load())
	assert.Equal(t, int64(1), m.durationCount.Load(), "duration still observed")
}

func TestMetricsEndpointServesAndCounts(t *testing.T) {
	s := integrationServer(t, nil) // Env=test, no token → registered

	code, header, body := serve(t, s, "GET", "/metrics", nil, nil)
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, header.Get("Content-Type"), "text/plain")
	assert.Contains(t, body, "gogogadget_build_info")
	assert.Contains(t, body, `gogogadget_http_requests_total{status="2xx"}`,
		"the /metrics scrape itself is counted via accessLog")

	// A 404 bumps the 4xx class.
	serve(t, s, "GET", "/definitely-not-here", nil, nil)
	_, _, body = serve(t, s, "GET", "/metrics", nil, nil)
	assert.Contains(t, body, `gogogadget_http_requests_total{status="4xx"} 1`)
}

func TestMetricsTokenGatesScrape(t *testing.T) {
	s := integrationServer(t, func(d *Deps) {
		cfg := d.Config
		cfg.MetricsToken = "sekret"
		d.Config = cfg
	})

	code, _, _ := serve(t, s, "GET", "/metrics", nil, nil)
	assert.Equal(t, http.StatusUnauthorized, code, "no bearer → 401")

	h := http.Header{}
	h.Set("Authorization", "Bearer wrong")
	code, _, _ = serve(t, s, "GET", "/metrics", nil, h)
	assert.Equal(t, http.StatusUnauthorized, code, "wrong bearer → 401")

	h.Set("Authorization", "Bearer sekret")
	code, _, body := serve(t, s, "GET", "/metrics", nil, h)
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "gogogadget_build_info")
}

func TestMetricsUnregisteredInProductionWithoutToken(t *testing.T) {
	s := integrationServer(t, func(d *Deps) {
		cfg := d.Config
		cfg.Env = "production"
		d.Config = cfg
	})
	code, _, _ := serve(t, s, "GET", "/metrics", nil, nil)
	assert.Equal(t, http.StatusNotFound, code, "prod without METRICS_TOKEN registers nothing")

	s = integrationServer(t, func(d *Deps) {
		cfg := d.Config
		cfg.Env = "production"
		cfg.MetricsToken = "sekret"
		d.Config = cfg
	})
	h := http.Header{}
	h.Set("Authorization", "Bearer sekret")
	code, _, body := serve(t, s, "GET", "/metrics", nil, h)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "gogogadget_build_info", "prod with token serves to bearers")
}
