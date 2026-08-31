// Package observability is the error-reporting seam: handlers and workers
// never import sentry-go (or any vendor). NoopReporter is the zero-account
// default; SentryReporter wraps sentry-go in one file.
package observability

import (
	"net/http"
)

// Reporter captures errors. Implementations are fire-and-forget by contract:
// a reporting failure must never surface to the caller.
type Reporter interface {
	Capture(err error)
	CaptureRequest(r *http.Request, err error)
}

// NoopReporter drops everything (unconfigured default).
type NoopReporter struct{}

func (NoopReporter) Capture(error)                       {}
func (NoopReporter) CaptureRequest(*http.Request, error) {}
