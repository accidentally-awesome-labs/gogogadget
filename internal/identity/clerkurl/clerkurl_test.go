package clerkurl

import "testing"

// The derived origin is what CSP connect-src allows, so a wrong answer here is
// a browser that silently cannot refresh the session JWT. Each case pins one
// rule of the derivation rather than the whole string once.
func TestFrontendAPIURL(t *testing.T) {
	for _, tc := range []struct {
		name        string
		environment string
		appURL      string
		want        string
	}{
		{"production strips the https scheme", "production", "https://app.example.com", "https://clerk.app.example.com"},
		{"production strips the http scheme", "production", "http://app.example.com", "https://clerk.app.example.com"},
		{"production keeps a port", "production", "https://app.example.com:8443", "https://clerk.app.example.com:8443"},
		{"development is the shared wildcard host", "development", "http://localhost:8080", "https://*.clerk.accounts.dev"},
		{"test is the shared wildcard host", "test", "http://localhost:18080", "https://*.clerk.accounts.dev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FrontendAPIURL(tc.environment, tc.appURL); got != tc.want {
				t.Fatalf("FrontendAPIURL(%q, %q) = %q, want %q", tc.environment, tc.appURL, got, tc.want)
			}
		})
	}
}
