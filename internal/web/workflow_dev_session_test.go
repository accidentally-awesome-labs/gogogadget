package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	identitydev "github.com/gogogadget/gogogadget/internal/identity/devadapter"
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
	_, err := s.q.GetIdentitySubject(t.Context(), sqlc.GetIdentitySubjectParams{Provider: "dev", Subject: "user_demo"})
	require.NoError(t, err)
	_, err = s.q.GetIdentityOrganization(t.Context(), sqlc.GetIdentityOrganizationParams{Provider: "dev", Subject: "org_demo"})
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
	require.Equal(t, "e2e:user_sw:org_sw:org:member", got, "cookie rewritten with the target org + membership role")
}

// nonMintingVerifier stands for any hosted identity adapter: it verifies the
// tokens it issued and knows nothing about synthetic sessions.
type nonMintingVerifier struct{}

func (nonMintingVerifier) Verify(context.Context, string) (*identity.ProviderClaims, error) {
	return nil, identity.ErrInvalidToken
}

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
	s := integrationServer(t, func(d *Deps) { d.Verifier = nonMintingVerifier{} })

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

// The minter must never become a second route to a dev session in production.
// The refusal is on the key, at config load, so a process that would have one
// does not start at all — whatever the selected adapter can do.
func TestSyntheticMinterDoesNotBypassTheProductionRefusal(t *testing.T) {
	var _ identity.SyntheticSessionMinter = identitydev.Verifier{}

	env := map[string]string{
		"APP_ENV": "production", "APP_URL": "https://app.example.com",
		"DATABASE_URL": "postgres://unused.example/production", "DEV_AUTH_BYPASS": "true",
	}
	_, err := config.LoadFrom(func(k string) string { return env[k] })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DEV_AUTH_BYPASS=true is refused when APP_ENV=production")
}
