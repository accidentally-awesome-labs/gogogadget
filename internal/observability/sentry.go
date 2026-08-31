package observability

import (
	"fmt"
	"net/http"

	"github.com/getsentry/sentry-go"
)

// SentryReporter reports to a module-owned Sentry hub. Keeping the hub on the
// reporter prevents one module from mutating or accidentally using process
// global Sentry state owned by another runtime.
type SentryReporter struct {
	hub *sentry.Hub
}

// NewSentryReporter constructs a reporter backed by the module-owned client.
func NewSentryReporter(client *sentry.Client) *SentryReporter {
	return &SentryReporter{hub: sentry.NewHub(client, sentry.NewScope())}
}

func (r *SentryReporter) Capture(err error) {
	if r == nil || r.hub == nil {
		return
	}
	r.hub.CaptureException(err)
}

func (r *SentryReporter) CaptureRequest(req *http.Request, err error) {
	if r == nil || r.hub == nil {
		return
	}
	r.hub.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("path", req.URL.Path)
		scope.SetContext("request", sentry.Context{"url": req.URL.String(), "method": req.Method})
		r.hub.CaptureException(err)
	})
}

// PanicError formats a recovered panic value.
func PanicError(rec any) error { return fmt.Errorf("panic: %v", rec) }
