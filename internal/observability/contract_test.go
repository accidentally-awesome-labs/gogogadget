package observability

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

var _ Reporter = NoopReporter{}

func runReporterContract(t *testing.T, factory func() Reporter) {
	t.Helper()
	t.Run("CaptureNeverPanics", func(t *testing.T) { factory().Capture(errors.New("contract-capture-boom")) })
	t.Run("CaptureRequestNeverPanics", func(t *testing.T) {
		factory().CaptureRequest(httptest.NewRequest(http.MethodGet, "/contract/path?q=1", nil), errors.New("contract-request-boom"))
	})
}
func TestNoopReporterContract(t *testing.T) {
	runReporterContract(t, func() Reporter { return NoopReporter{} })
}
