package analytics

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNoopCapturerContract(t *testing.T) {
	var c Capturer = NoopCapturer{}
	c.Capture("user", "event", map[string]any{"x": 1})
}
func TestIngestProxyRejectsInvalidEndpoint(t *testing.T) {
	_, err := IngestProxy("not a URL")
	if err == nil {
		t.Fatal("invalid endpoint accepted")
	}
	_ = errors.New("")
	_ = http.MethodGet
	_ = httptest.NewRecorder
}
