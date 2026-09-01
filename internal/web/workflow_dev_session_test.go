package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
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
