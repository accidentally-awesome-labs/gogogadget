// Package analytics contains the provider-neutral analytics capability.
package analytics

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

type Capturer interface {
	Capture(userID, event string, props map[string]any)
}
type BufferingCapturer interface {
	Capturer
	Close()
}
type NoopCapturer struct{}

func (NoopCapturer) Capture(string, string, map[string]any) {}

// IngestProxy reverse-proxies /ingest/* to the configured provider endpoint.
// The endpoint is intentionally represented by a URL, keeping provider SDKs
// out of this seam while preserving same-origin browser telemetry.
func IngestProxy(host string) (http.Handler, error) {
	target, err := url.Parse(host)
	if err != nil {
		return nil, err
	}
	if target.Scheme == "" || target.Host == "" {
		return nil, &url.Error{Op: "parse", URL: host, Err: errInvalidEndpoint}
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

type endpointError string

func (e endpointError) Error() string { return string(e) }

var errInvalidEndpoint error = endpointError("analytics: endpoint must include scheme and host")
