package observability

import (
	"errors"
	"net/http/httptest"
	"testing"
)

func TestNoopReporterNoOps(t *testing.T) {
	r := NoopReporter{}
	r.Capture(errors.New("x"))                                        // must not panic
	r.CaptureRequest(httptest.NewRequest("GET", "/", nil), errors.New("y"))
}

func TestPanicErrorFormats(t *testing.T) {
	if got := PanicError("boom"); got.Error() != "panic: boom" {
		t.Fatalf("PanicError = %q", got)
	}
}
