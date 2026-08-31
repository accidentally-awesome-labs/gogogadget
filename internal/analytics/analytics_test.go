package analytics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIngestProxyRewritesPathAndHost(t *testing.T) {
	var gotPath, gotHost, gotMethod string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHost = r.Host
		gotMethod = r.Method
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()

	proxy, err := IngestProxy(upstream.URL)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/ingest/e/?ip=1", nil)
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "/e/", gotPath, "/ingest prefix must be stripped")
	assert.Equal(t, "POST", gotMethod)
	assert.NotEqual(t, "example.com", gotHost, "Host must be rewritten to the upstream")
}

func TestNoopCapturer(t *testing.T) {
	NoopCapturer{}.Capture("u", "event", map[string]any{"k": "v"}) // must not panic
}
