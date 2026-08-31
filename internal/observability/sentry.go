package observability

import (
	"context"
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

// NewSentryReporter constructs a reporter backed by client. The variadic form
// preserves the old no-argument API for callers that only need a no-op-safe
// reporter in tests; configured modules always pass their own client.
func NewSentryReporter(client ...*sentry.Client) *SentryReporter {
	var c *sentry.Client
	if len(client) > 0 {
		c = client[0]
	}
	return &SentryReporter{hub: sentry.NewHub(c, sentry.NewScope())}
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

// flush waits for this reporter's module-owned client to deliver queued events.
func (r *SentryReporter) flush(ctx context.Context) error {
	if r == nil || r.hub == nil || r.hub.Client() == nil {
		return nil
	}
	if r.hub.FlushWithContext(ctx) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return context.DeadlineExceeded
}

// PanicError formats a recovered panic value.
func PanicError(rec any) error { return fmt.Errorf("panic: %v", rec) }
