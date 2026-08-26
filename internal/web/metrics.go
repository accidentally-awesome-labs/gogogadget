package web

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

// metricsRegistry is the process-local Prometheus registry. Stdlib only —
// the text exposition format is written by hand (no client_golang
// dependency; see the repo's no-new-deps rule). Counters are atomics; gauges
// are computed at scrape time.
type metricsRegistry struct {
	// requests[i] counts responses with status class i+2 (2xx..5xx).
	requests [4]atomic.Int64
	// Other classes (1xx) are negligible and uncounted by design.
	durationSumNano atomic.Int64
	durationCount   atomic.Int64
}

func (m *metricsRegistry) observe(status int, d time.Duration) {
	if c := status/100 - 2; c >= 0 && c < len(m.requests) {
		m.requests[c].Add(1)
	}
	m.durationSumNano.Add(int64(d))
	m.durationCount.Add(1)
}

// render emits the Prometheus text exposition format (version 0.0.4).
func (m *metricsRegistry) render(version string, pool *poolStats) string {
	var b strings.Builder
	w := func(line string) { b.WriteString(line); b.WriteByte('\n') }

	w(`# HELP gogogadget_http_requests_total HTTP requests by response status class.`)
	w(`# TYPE gogogadget_http_requests_total counter`)
	for i, label := range [4]string{"2xx", "3xx", "4xx", "5xx"} {
		w(fmt.Sprintf("gogogadget_http_requests_total{status=%q} %d", label, m.requests[i].Load()))
	}

	w(`# HELP gogogadget_http_request_duration_seconds HTTP request durations in seconds.`)
	w(`# TYPE gogogadget_http_request_duration_seconds counter`)
	w(fmt.Sprintf("gogogadget_http_request_duration_seconds_count %d", m.durationCount.Load()))
	w(fmt.Sprintf("gogogadget_http_request_duration_seconds_sum %.9f", float64(m.durationSumNano.Load())/1e9))

	// Runtime gauges.
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	gauge := func(name, help string, v uint64) {
		w(`# HELP ` + name + ` ` + help)
		w(`# TYPE ` + name + ` gauge`)
		w(fmt.Sprintf("%s %d", name, v))
	}
	gauge("go_goroutines", "Number of goroutines.", uint64(runtime.NumGoroutine()))
	gauge("go_memstats_alloc_bytes", "Bytes allocated and still in use.", ms.Alloc)
	gauge("go_memstats_sys_bytes", "Bytes obtained from the OS.", ms.Sys)
	gauge("go_memstats_heap_inuse_bytes", "Heap bytes in use.", ms.HeapInuse)

	if pool != nil {
		gauge("gogogadget_db_pool_acquired_conns", "Connections acquired by requests.", uint64(pool.acquired))
		gauge("gogogadget_db_pool_idle_conns", "Idle connections.", uint64(pool.idle))
		gauge("gogogadget_db_pool_total_conns", "Total connections.", uint64(pool.total))
		gauge("gogogadget_db_pool_max_conns", "Connection limit.", uint64(pool.max))
	}

	w(`# HELP gogogadget_build_info Build metadata.`)
	w(`# TYPE gogogadget_build_info gauge`)
	w(fmt.Sprintf("gogogadget_build_info{version=%q} 1", version))
	return b.String()
}

// poolStats decouples the registry from pgxpool (and keeps render testable
// without a live pool).
type poolStats struct {
	acquired, idle, total, max int32
}

// GET /metrics — Prometheus scrape. Registered when a METRICS_TOKEN is set
// (bearer-gated) or outside production; production without a token registers
// nothing — internal Go stats must not be public by default.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if s.cfg.MetricsToken != "" {
		// Constant-time: `!=` on strings returns at the first differing byte, so
		// a scraper endpoint with no rate limit (it is RateExempt by policy) hands
		// an attacker a timing oracle over the token.
		presented := []byte(r.Header.Get("Authorization"))
		expected := []byte("Bearer " + s.cfg.MetricsToken)
		if subtle.ConstantTimeCompare(presented, expected) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
			http.Error(w, "metrics token required", http.StatusUnauthorized)
			return
		}
	}
	var pool *poolStats
	if s.db != nil {
		st := s.db.Stat()
		pool = &poolStats{acquired: int32(st.AcquiredConns()), idle: int32(st.IdleConns()), total: int32(st.TotalConns()), max: int32(st.MaxConns())}
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(s.metrics.render(s.version, pool)))
}
