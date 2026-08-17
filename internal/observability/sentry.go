package observability

import (
	"fmt"
	"net/http"

	"github.com/getsentry/sentry-go"
)

// SentryReporter reports to Sentry. This is the only internal package that
// imports sentry-go (besides cmd/server's sentry.Init/Flush).
type SentryReporter struct{}

func NewSentryReporter() *SentryReporter { return &SentryReporter{} }

func (SentryReporter) Capture(err error) {
	sentry.CaptureException(err)
}

func (SentryReporter) CaptureRequest(r *http.Request, err error) {
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("path", r.URL.Path)
		scope.SetContext("request", sentry.Context{"url": r.URL.String(), "method": r.Method})
		sentry.CaptureException(err)
	})
}

// PanicError formats a recovered panic value.
func PanicError(rec any) error { return fmt.Errorf("panic: %v", rec) }
