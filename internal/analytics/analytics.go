// Package analytics is the PostHog seam: server-side capture (no-op without
// a key) plus the /ingest reverse proxy that lets the client SDK load under
// script-src 'self'.
package analytics

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/posthog/posthog-go"
)

// Capturer records product events. The no-op implementation means every call
// site is unconditional.
type Capturer interface {
	Capture(userID, event string, props map[string]any)
}

// NoopCapturer discards events (POSTHOG_API_KEY empty).
type NoopCapturer struct{}

func (NoopCapturer) Capture(string, string, map[string]any) {}

// PostHogCapturer queues events via posthog-go.
type PostHogCapturer struct {
	client posthog.Client
}

func NewPostHog(apiKey, host string) (*PostHogCapturer, error) {
	client, err := posthog.NewWithConfig(apiKey, posthog.Config{Endpoint: host})
	if err != nil {
		return nil, err
	}
	return &PostHogCapturer{client: client}, nil
}

func (c *PostHogCapturer) Capture(userID, event string, props map[string]any) {
	p := posthog.NewProperties()
	for k, v := range props {
		p.Set(k, v)
	}
	// Fire-and-forget: queueing failure must never affect the request.
	_ = c.client.Enqueue(posthog.Capture{DistinctId: userID, Event: event, Properties: p})
}

// Close flushes queued events (called on shutdown).
func (c *PostHogCapturer) Close() { _ = c.client.Close() }

// IngestProxy reverse-proxies /ingest/* to the PostHog host (PostHog's
// recommended proxy pattern): the client SDK and event posts stay same-origin,
// so CSP remains script-src 'self' and ad-blockers don't matter.
func IngestProxy(host string) (http.Handler, error) {
	target, err := url.Parse(host)
	if err != nil {
		return nil, err
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	base := proxy.Director
	proxy.Director = func(r *http.Request) {
		base(r)
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/ingest")
		r.URL.RawPath = ""
		r.Host = target.Host
	}
	return proxy, nil
}
