package web

import (
	"net/http"
	"net/url"
	"strings"
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
	// Reason is mandatory (10–280 chars) — it lands on the session row and
	// in both audit entries.
	form := url.Values{"reason": []string{"Ticket #99 — reproducing the reported bug"}}
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
	reason := url.Values{"reason": []string{"Ticket #100 — checking the disabled-account guard"}}
	code, _, _ := serve(t, s, "POST", "/admin/users/user_dis_target/impersonate", []byte(reason.Encode()), h,
		append(csrfCookies, sessionCookie("user_dis_admin", "org_dis_admin", "org:admin"))...)
	assert.Equal(t, http.StatusUnprocessableEntity, code, "disabled target rejected even with a valid reason")
}

func TestImpersonationAdminDemotedMidSession(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_dem_admin", "org_dem_admin")
	seedMembership(t, s, "user_dem_target", "org_dem_target", "org:admin")
	adminCookie := sessionCookie("user_dem_admin", "org_dem_admin", "org:admin")

	imp := startImpersonation(t, s, adminCookie, "user_dem_target")

	// Demote the admin mid-session…
	require.NoError(t, s.q.SetUserAdminRoleByEmail(t.Context(), sqlc.SetUserAdminRoleByEmailParams{
		Email: "user_dem_admin@example.com", AdminRole: "",
	}))

	// …the next request clears the cookie and proceeds as the (former) admin.
	code, _, body := serve(t, s, "GET", "/app", nil, nil, adminCookie, imp)
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "impersonation-banner")
}

func TestImpersonationRequiresReason(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_rsn_admin", "org_rsn")
	seedMembership(t, s, "user_rsn_target", "org_rsn", "org:member")
	adminCookie := sessionCookie("user_rsn_admin", "org_rsn", "org:admin")

	// Interstitial renders the target, the org picker, and the reason field.
	code, _, body := serve(t, s, "GET", "/admin/users/user_rsn_target/impersonate", nil, nil, adminCookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `data-testid="impersonate-form"`)
	assert.Contains(t, body, `data-testid="impersonate-reason"`)
	assert.Contains(t, body, "user_rsn_target@example.com")
	assert.Contains(t, body, "org_rsn")

	post := func(reason string) (int, string) {
		token, csrfCookies := csrfFor(t, s)
		h := http.Header{}
		h.Set("X-CSRF-Token", token)
		h.Set("Content-Type", "application/x-www-form-urlencoded")
		form := url.Values{"reason": []string{reason}}
		code, _, body := serve(t, s, "POST", "/admin/users/user_rsn_target/impersonate",
			[]byte(form.Encode()), h, append(csrfCookies, adminCookie)...)
		return code, body
	}

	// Missing / too short / whitespace-only → 422, nothing started.
	for _, bad := range []string{"", "too short", "         "} {
		code, body := post(bad)
		assert.Equal(t, http.StatusUnprocessableEntity, code, "reason %q", bad)
		assert.Contains(t, body, `data-testid="impersonate-error"`)
	}
	sessions, err := s.q.ListAuditAll(t.Context(), sqlc.ListAuditAllParams{Filter: "impersonation.start", Off: 0, Lim: 10})
	require.NoError(t, err)
	assert.Empty(t, sessions, "no session started while the reason is invalid")

	// Over the 280-char cap → 422 as well.
	code, _ = post(strings.Repeat("x", 281))
	assert.Equal(t, http.StatusUnprocessableEntity, code)

	// Valid reason → 303 to /app (plain form post: an auth-boundary switch is
	// a hard navigation), session row + start audit carry the reason.
	reason := "Ticket #4242 — customer cannot export projects"
	code, _ = post(reason)
	require.Equal(t, http.StatusSeeOther, code)

	rows, err := s.q.ListAuditAll(t.Context(), sqlc.ListAuditAllParams{Filter: "impersonation.start", Off: 0, Lim: 10})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Contains(t, string(rows[0].Metadata), reason, "reason recorded in the audit trail")

	// …and on the session row itself.
	var stored string
	require.NoError(t, s.db.QueryRow(t.Context(),
		`SELECT reason FROM impersonation_sessions WHERE target_user_id = $1`, "user_rsn_target").Scan(&stored))
	assert.Equal(t, reason, stored)
}

func TestImpersonationStopAuditCarriesReason(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_stp_admin", "org_stp")
	seedMembership(t, s, "user_stp_target", "org_stp", "org:member")
	adminCookie := sessionCookie("user_stp_admin", "org_stp", "org:admin")

	imp := startImpersonation(t, s, adminCookie, "user_stp_target")

	token, csrfCookies := csrfFor(t, s)
	h := http.Header{}
	h.Set("X-CSRF-Token", token)
	h.Set("HX-Request", "true")
	code, _, _ := serve(t, s, "POST", "/app/impersonation/exit", nil, h,
		append(csrfCookies, adminCookie, imp)...)
	require.Equal(t, http.StatusOK, code)

	rows, err := s.q.ListAuditAll(t.Context(), sqlc.ListAuditAllParams{Filter: "impersonation.stop", Off: 0, Lim: 10})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Contains(t, string(rows[0].Metadata), "Ticket #99", "stop entry reads standalone: it repeats the reason")
	assert.Contains(t, string(rows[0].Metadata), "user_stp_target")
}

// The impersonation cookie must not be a bearer token. Every other check in
// applyImpersonation validates a property of the session ROW, so without a
// subject binding, possession of the id IS the authorization - and the id is not
// secret enough for that. It is an opaque database id that lives for two hours
// in a cookie any user can set in their own browser.
//
// The escalation this closes: the id used to be written into the org-scoped
// impersonation.start audit entry, and /app/activity renders full audit metadata
// to every member of that organization. So an ordinary member could read it and
// assume the identity of whoever support was helping - typically the org owner.
func TestImpersonationCookieIsRefusedForAnyoneButItsAdmin(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_bind_admin", "org_bind_admin")
	seedMembership(t, s, "user_bind_target", "org_bind", "org:admin")
	seedMembership(t, s, "user_bind_member", "org_bind", "org:member")

	adminCookie := sessionCookie("user_bind_admin", "org_bind_admin", "org:admin")
	imp := startImpersonation(t, s, adminCookie, "user_bind_target")

	// The attacker is an ordinary member of the target organization presenting a
	// stolen id alongside their OWN verified session.
	code, _, body := serve(t, s, "GET", "/app", nil, nil,
		sessionCookie("user_bind_member", "org_bind", "org:member"), imp)
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "impersonation-banner",
		"an unrelated member presenting the id must stay themselves; a switched identity means the cookie is a bearer token")

	// The admin who started it still works, so the binding is a check, not a ban.
	code, _, adminBody := serve(t, s, "GET", "/app", nil, nil, adminCookie, imp)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, adminBody, "impersonation-banner",
		"the admin who started the session must still be able to use it")
}

// The credential must not be published to the people it can be used against:
// the start entry is org-scoped and /app/activity renders its whole metadata
// blob to every member of that organization.
func TestImpersonationAuditDoesNotPublishTheSessionID(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_audit_admin", "org_audit_admin")
	seedMembership(t, s, "user_audit_target", "org_audit", "org:admin")

	adminCookie := sessionCookie("user_audit_admin", "org_audit_admin", "org:admin")
	imp := startImpersonation(t, s, adminCookie, "user_audit_target")
	require.NotEmpty(t, imp.Value)

	rows, err := s.q.ListAuditAll(t.Context(), sqlc.ListAuditAllParams{Filter: "impersonation.start", Off: 0, Lim: 10})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.NotContains(t, string(rows[0].Metadata), imp.Value,
		"the entry is org-scoped and its metadata is rendered to every member, so it must not carry a live credential")
	assert.Equal(t, "org_audit", rows[0].ClerkOrgID.String,
		"the entry must be findable in the target org's own feed")
}
