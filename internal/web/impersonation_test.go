package web

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func startImpersonation(t *testing.T, s *Server, adminCookie *http.Cookie, targetID string) *http.Cookie {
	t.Helper()
	token, csrfCookies := csrfFor(t, s)
	form := url.Values{}
	h := http.Header{}
	h.Set("X-CSRF-Token", token)
	h.Set("Content-Type", "application/x-www-form-urlencoded")
	h.Set("HX-Request", "true")
	code, hdr, _ := serve(t, s, "POST", "/admin/users/"+targetID+"/impersonate", []byte(form.Encode()), h, append(csrfCookies, adminCookie)...)
	require.Equal(t, http.StatusOK, code, "impersonate POST accepted")

	var imp *http.Cookie
	for _, c := range hdr["Set-Cookie"] {
		if parsed := parseSetCookie(t, c); parsed.Name == impersonationCookieName {
			imp = parsed
		}
	}
	require.NotNil(t, imp, "ggg_imp cookie set")
	require.NotEmpty(t, imp.Value)
	return &http.Cookie{Name: impersonationCookieName, Value: imp.Value}
}

func TestImpersonationFlow(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_imp_admin", "org_imp_admin")
	seedMembership(t, s, "user_imp_target", "org_imp_target", "org:admin")
	adminCookie := sessionCookie("user_imp_admin", "org_imp_admin", "org:admin")
	targetCookie := sessionCookie("user_imp_target", "org_imp_target", "org:admin")

	imp := startImpersonation(t, s, adminCookie, "user_imp_target")

	// Next request as admin + cookie → identity is the TARGET (banner shows).
	code, _, body := serve(t, s, "GET", "/app", nil, nil, adminCookie, imp)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `data-testid="impersonation-banner"`)
	assert.Contains(t, body, "org_imp_target", "org switcher shows the target org")
	assert.Contains(t, body, "user_imp_target")

	// /admin correctly 403s mid-impersonation (target is not a site admin).
	code, _, _ = serve(t, s, "GET", "/admin", nil, nil, adminCookie, imp)
	assert.Equal(t, http.StatusForbidden, code)

	// Exit → session ended, cookie cleared, hard redirect to /admin.
	token, csrfCookies := csrfFor(t, s)
	h := http.Header{}
	h.Set("X-CSRF-Token", token)
	h.Set("Content-Type", "application/x-www-form-urlencoded")
	h.Set("HX-Request", "true")
	code, hdr, _ := serve(t, s, "POST", "/app/impersonation/exit", nil, h, append(csrfCookies, adminCookie, imp)...)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "/admin", hdr.Get("HX-Redirect"))

	sess, err := s.q.GetImpersonationSession(t.Context(), imp.Value)
	require.NoError(t, err)
	assert.True(t, sess.EndedAt.Valid, "session marked ended")

	// Admin identity restored.
	code, _, body = serve(t, s, "GET", "/admin", nil, nil, adminCookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "Total users")
	_ = targetCookie
}

func TestImpersonationExpiredSessionSelfClears(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_exp_admin", "org_exp_admin")
	seedMembership(t, s, "user_exp_target", "org_exp_target", "org:admin")

	sess, err := s.q.InsertImpersonationSession(t.Context(), sqlc.InsertImpersonationSessionParams{
		ID: "expired-session-1", AdminUserID: "user_exp_admin",
		TargetUserID: "user_exp_target", TargetOrgID: "org_exp_target",
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	})
	require.NoError(t, err)

	adminCookie := sessionCookie("user_exp_admin", "org_exp_admin", "org:admin")
	imp := &http.Cookie{Name: impersonationCookieName, Value: sess.ID}
	code, hdr, body := serve(t, s, "GET", "/app", nil, nil, adminCookie, imp)
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "impersonation-banner", "expired session does not impersonate")
	assert.Contains(t, body, "org_exp_admin", "admin identity kept")

	cleared := false
	for _, c := range hdr["Set-Cookie"] {
		if p := parseSetCookie(t, c); p.Name == impersonationCookieName && p.Value == "" {
			cleared = true
		}
	}
	assert.True(t, cleared, "expired session cookie cleared")
}

func TestImpersonationNonAdmin403(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_na1", "org_na1", "org:admin")
	seedMembership(t, s, "user_na2", "org_na2", "org:admin")

	token, csrfCookies := csrfFor(t, s)
	h := http.Header{}
	h.Set("X-CSRF-Token", token)
	h.Set("Content-Type", "application/x-www-form-urlencoded")
	h.Set("HX-Request", "true")
	code, _, _ := serve(t, s, "POST", "/admin/users/user_na2/impersonate", nil, h,
		append(csrfCookies, sessionCookie("user_na1", "org_na1", "org:admin"))...)
	assert.Equal(t, http.StatusForbidden, code)
}

func TestImpersonationDisabledTarget422(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_dis_admin", "org_dis_admin")
	seedMembership(t, s, "user_dis_target", "org_dis_target", "org:admin")
	require.NoError(t, s.q.SetUserDisabled(t.Context(), sqlc.SetUserDisabledParams{
		ClerkUserID: "user_dis_target", DisabledAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}))

	token, csrfCookies := csrfFor(t, s)
	h := http.Header{}
	h.Set("X-CSRF-Token", token)
	h.Set("Content-Type", "application/x-www-form-urlencoded")
	h.Set("HX-Request", "true")
	code, _, _ := serve(t, s, "POST", "/admin/users/user_dis_target/impersonate", nil, h,
		append(csrfCookies, sessionCookie("user_dis_admin", "org_dis_admin", "org:admin"))...)
	assert.Equal(t, http.StatusUnprocessableEntity, code)
}

func TestImpersonationAdminDemotedMidSession(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_dem_admin", "org_dem_admin")
	seedMembership(t, s, "user_dem_target", "org_dem_target", "org:admin")
	adminCookie := sessionCookie("user_dem_admin", "org_dem_admin", "org:admin")

	imp := startImpersonation(t, s, adminCookie, "user_dem_target")

	// Demote the admin mid-session…
	require.NoError(t, s.q.SetUserAdminByEmail(t.Context(), sqlc.SetUserAdminByEmailParams{
		Email: "user_dem_admin@example.com", IsAdmin: false,
	}))

	// …the next request clears the cookie and proceeds as the (former) admin.
	code, _, body := serve(t, s, "GET", "/app", nil, nil, adminCookie, imp)
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "impersonation-banner")
}
