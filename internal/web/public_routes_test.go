package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/stretchr/testify/assert"
)

// Untested public routes: legal pages, SEO endpoints, probes.

func TestPublicRoutesServe(t *testing.T) {
	s := integrationServer(t, nil)

	for _, tc := range []struct{ path, marker string }{
		{"/terms", "Terms of Service"},
		{"/privacy", "Privacy Policy"},
	} {
		code, _, body := serve(t, s, "GET", tc.path, nil, nil)
		assert.Equal(t, http.StatusOK, code, tc.path)
		assert.Contains(t, body, tc.marker, tc.path)
	}
}

func TestRobotsTxt(t *testing.T) {
	s := integrationServer(t, nil)
	code, _, body := serve(t, s, "GET", "/robots.txt", nil, nil)
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "Sitemap:", "robots points at the sitemap")
}

func TestHealthAndReadyProbes(t *testing.T) {
	s := integrationServer(t, nil)
	for _, path := range []string{"/healthz", "/readyz"} {
		code, _, body := serve(t, s, "GET", path, nil, nil)
		assert.Equal(t, http.StatusOK, code, path)
		assert.Contains(t, body, `"status":"ok"`, path)
	}
}

func TestLogoutDevBranchClearsCookie(t *testing.T) {
	s := integrationServer(t, nil) // DEV_AUTH_BYPASS without Clerk → dev branch

	code, header, _ := serve(t, s, "GET", "/logout", nil, nil, sessionCookie("user_lo", "org_lo", "org:member"))
	assert.Equal(t, http.StatusSeeOther, code)
	assert.Equal(t, "/", header.Get("Location"))
	var cleared bool
	for _, c := range header.Values("Set-Cookie") {
		if strings.Contains(c, "__session=") && strings.Contains(c, "Max-Age=0") {
			cleared = true
		}
	}
	assert.True(t, cleared, "session cookie must be expired")
}

func TestBillingPortalRedirectsWithMock(t *testing.T) {
	s := integrationServer(t, func(d *Deps) {
		d.Config.PolarAccessToken = "pol_test"
		d.Billing = &billing.MockClient{PortalURL: "https://portal.example.test/session"}
	})
	seedMembership(t, s, "user_bp", "org_bp", "org:admin")

	token, csrfCookies := csrfFor(t, s)
	h := http.Header{}
	h.Set("X-CSRF-Token", token)
	h.Set("HX-Request", "true")
	code, header, _ := serve(t, s, "POST", "/app/billing/portal", nil, h, append(csrfCookies, sessionCookie("user_bp", "org_bp", "org:admin"))...)
	assert.Equal(t, http.StatusOK, code, "HX-Redirect rides a 200")
	assert.Contains(t, header.Get("HX-Redirect"), "https://portal.example.test/session")
}
