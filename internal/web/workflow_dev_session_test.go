package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The two zero-account handlers, tested with the module that owns them. They
// used to live in system/server's public-route suite, which meant removing
// workflow/dev-session left a derivative holding tests for handlers it no
// longer had.

func TestDevLoginSetsCookieAndRedirects(t *testing.T) {
	s := integrationServer(t, nil)

	code, header, _ := serve(t, s, "GET", "/dev/login", nil, nil)
	assert.Equal(t, http.StatusSeeOther, code)
	assert.Contains(t, header.Get("Location"), "/app")
	var set string
	for _, c := range header.Values("Set-Cookie") {
		if strings.HasPrefix(c, "__session=") {
			set = strings.SplitN(strings.TrimPrefix(c, "__session="), ";", 2)[0]
		}
	}
	require.NotEmpty(t, set, "dev login sets the synthetic session cookie")
	code, _, _ = serve(t, s, "GET", "/app", nil, nil, &http.Cookie{Name: sessionCookieName, Value: set})
	require.Equal(t, http.StatusOK, code)
	_, err := s.q.GetIdentitySubject(t.Context(), sqlc.GetIdentitySubjectParams{Provider: identity.MockProvider, Subject: "user_demo"})
	require.NoError(t, err)
	_, err = s.q.GetIdentityOrganization(t.Context(), sqlc.GetIdentityOrganizationParams{Provider: identity.MockProvider, Subject: "org_demo"})
	require.NoError(t, err)
}

func TestDevSwitchOrgRewritesRole(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_sw", "org_sw", "org:member")

	code, header, _ := serve(t, s, "GET", "/dev/switch-org?org=org_sw", nil, nil, sessionCookie("user_sw", "org_other", "org:member"))
	assert.Equal(t, http.StatusSeeOther, code)
	var got string
	for _, c := range header.Values("Set-Cookie") {
		if strings.HasPrefix(c, "__session=") {
			got = strings.SplitN(strings.TrimPrefix(c, "__session="), ";", 2)[0]
		}
	}
	want, mintErr := identity.MockVerifier{}.MintSession("user_sw", "org_sw", "org:member")
	require.NoError(t, mintErr)
	require.Equal(t, want, got, "cookie rewritten with the target org + membership role, minted through the selected adapter")
}

// The stand-in for any hosted identity adapter is the seam's own
// MockHostedVerifier: it verifies the tokens it issued and knows nothing
// about synthetic sessions. It has to be a distinct type rather than a
// flag, because whether an adapter can mint is a method-set property.

// The zero-account dev surface depends on the identity adapter selected for
// this environment being able to mint a synthetic session. That dependency
// cannot be a manifest `requires` — an adapter is a per-environment choice, so
// requiring one would pin it into every install and make deselecting it
// refuse — so it is an optional seam interface plus this refusal.
//
// What it replaces: dev-session used to write "e2e:"+… itself, which compiled
// against any adapter and produced a cookie the selected verifier rejects, so
// /dev/login redirected to /app, /app bounced to /login, and nothing anywhere
// said why. The failure has to be named.
func TestDevLoginRefusesLoudlyWithoutASyntheticSessionMinter(t *testing.T) {
	s := integrationServer(t, func(d *Deps) { d.Verifier = identity.MockHostedVerifier{} })

	code, hdr, body := serve(t, s, "GET", "/dev/login", nil, nil)
	require.Equal(t, http.StatusServiceUnavailable, code,
		"a dev surface that cannot mint a session must say so, not redirect into a loop")
	for _, c := range hdr.Values("Set-Cookie") {
		assert.NotContains(t, c, sessionCookieName+"=",
			"no session cookie may be issued that nothing can verify")
	}
	assert.Contains(t, body, "zero-account identity adapter",
		"the response must name the missing capability")
}

// The mint route is the e2e harness's only path to an authenticated context,
// and it exists so that no TypeScript has to know the token's grammar. What it
// hands back must therefore be a token the SELECTED adapter verifies —
// asserted here by comparing against the port's own output and then spending
// the cookie on a guarded page, because a cookie that parses and a cookie
// that authenticates are not the same claim.
func TestDevSessionMintsACookieTheSelectedAdapterVerifies(t *testing.T) {
	s := integrationServer(t, nil)

	code, header, _ := serve(t, s, "GET", "/dev/session?user=user_mint&org=org_mint&role=org:admin", nil, nil)
	require.Equal(t, http.StatusNoContent, code, "the harness reads the cookie, not a page")
	var got string
	for _, c := range header.Values("Set-Cookie") {
		if strings.HasPrefix(c, sessionCookieName+"=") {
			got = strings.SplitN(strings.TrimPrefix(c, sessionCookieName+"="), ";", 2)[0]
		}
	}
	require.NotEmpty(t, got, "the route's whole output is the session cookie")
	want, mintErr := identity.MockVerifier{}.MintSession("user_mint", "org_mint", "org:admin")
	require.NoError(t, mintErr)
	assert.Equal(t, want, got, "minted through the selected adapter, never assembled here")

	code, _, _ = serve(t, s, "GET", "/app", nil, nil, &http.Cookie{Name: sessionCookieName, Value: got})
	require.Equal(t, http.StatusOK, code, "the cookie must authenticate, not merely parse")
}

// The asymmetry that made the duplicated grammar dangerous, gone by
// construction. MintSession refuses a subject containing the grammar's own
// separator; the deleted TypeScript template did not, so a persona id
// carrying a ':' yielded a token that split wrong and a cookie nothing could
// verify. The route now answers the adapter's own refusal — as a CALLER error
// rather than the 503 below, which means "this deployment cannot mint at
// all". Conflating the two would name the wrong cause.
func TestDevSessionRefusesASubjectCarryingTheGrammarSeparator(t *testing.T) {
	s := integrationServer(t, nil)

	code, header, body := serve(t, s, "GET", "/dev/session?user=user%3Aevil&org=org_mint&role=org:admin", nil, nil)
	require.Equal(t, http.StatusBadRequest, code,
		"a subject the adapter will not mint is the caller's error, not a missing capability")
	assert.Contains(t, body, "may not contain ':'", "the refusal names what is wrong with the subject")
	for _, c := range header.Values("Set-Cookie") {
		assert.NotContains(t, c, sessionCookieName+"=", "no cookie for a subject the adapter refused")
	}
}

// The refusal has to reach the harness, which is the whole reason this route
// is worth more than deduplication. A derivative selecting a hosted identity
// adapter for its test environment has no minter at all, and the harness must
// be told which capability is missing, in a body it can print, rather than
// carrying a cookie nothing verifies and reporting a bounce to /login.
//
// The two browser routes render the NotConfigured page for this. A harness is
// not a browser, so this route answers the same refusal as text — one string,
// devSessionCapability, names it on both paths.
func TestDevSessionNamesTheMissingCapabilityForAHostedAdapter(t *testing.T) {
	s := integrationServer(t, func(d *Deps) { d.Verifier = identity.MockHostedVerifier{} })

	code, header, body := serve(t, s, "GET", "/dev/session?user=user_mint&org=org_mint&role=org:admin", nil, nil)
	require.Equal(t, http.StatusServiceUnavailable, code,
		"a harness that cannot get a session must be told so, not redirected into a loop")
	assert.Contains(t, body, devSessionCapability,
		"the harness prints this body, so it must name the capability the selected adapter lacks")
	for _, c := range header.Values("Set-Cookie") {
		assert.NotContains(t, c, sessionCookieName+"=", "no session cookie that nothing can verify")
	}
}

// The minter must never become a second route to a dev session in production.
// The refusal is on the key, at config load, so a process that would have one
// does not start at all — whatever the selected adapter can do.
func TestSyntheticMinterDoesNotBypassTheProductionRefusal(t *testing.T) {
	env := map[string]string{
		"APP_ENV": "production", "APP_URL": "https://app.example.com",
		"DATABASE_URL": "postgres://unused.example/production", "DEV_AUTH_BYPASS": "true",
	}
	_, err := config.LoadFrom(func(k string) string { return env[k] })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DEV_AUTH_BYPASS=true is refused when APP_ENV=production")
}
