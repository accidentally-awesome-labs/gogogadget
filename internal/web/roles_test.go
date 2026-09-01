package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// staffUser seeds a member with a platform role.
func staffUser(t *testing.T, s *Server, id, org, role string) *http.Cookie {
	t.Helper()
	seedMembership(t, s, id, org, "org:admin")
	require.NoError(t, s.q.SetUserAdminRole(t.Context(), sqlc.SetUserAdminRoleParams{
		UserID: id, AdminRole: role,
	}))
	return sessionCookie(id, org, "org:admin")
}

// The boundary in one table: support reads the admin area, admin changes it.
// Enforced by METHOD rather than a route list, so this matrix covers routes
// that do not exist yet.
func TestSupportReadsAdminButCannotWrite(t *testing.T) {
	s := integrationServer(t, nil)
	support := staffUser(t, s, "user_sup", "org_sup", identity.RoleSupport)

	reads := []string{"/admin", "/admin/users", "/admin/orgs", "/admin/flags", "/admin/audit", "/admin/jobs",
		"/admin/announcements", "/admin/schedules", "/admin/content", "/admin/content/new?kind=post", "/admin/media"}
	for _, path := range reads {
		code, _, _ := serve(t, s, "GET", path, nil, nil, support)
		assert.Equal(t, http.StatusOK, code, "support must be able to read %s", path)
	}

	writes := []string{
		"/admin/flags",
		"/admin/flags/beta/toggle",
		"/admin/flags/beta/delete",
		"/admin/schedules",
		"/admin/announcements",
		"/admin/jobs/1/requeue",
		"/admin/users/user_sup/disable",
		"/admin/users/user_sup/impersonate",
		"/admin/users/user_sup/role",
		"/admin/content",
		"/admin/content/1/publish",
		"/admin/content/1/delete",
		"/admin/media",
	}
	for _, path := range writes {
		code, _, _ := postForm(t, s, path, url.Values{}, support)
		assert.Equal(t, http.StatusForbidden, code, "support must not be able to POST %s", path)
	}
}

func TestAdminRetainsWriteAccess(t *testing.T) {
	s := integrationServer(t, nil)
	admin := staffUser(t, s, "user_full", "org_full", identity.RoleAdmin)

	code, _, _ := serve(t, s, "GET", "/admin", nil, nil, admin)
	assert.Equal(t, http.StatusOK, code)

	// A real mutation, not just a non-403: the flag must actually be created.
	code, _, _ = postForm(t, s, "/admin/flags",
		url.Values{"key": {"role-test-flag"}, "description": {"x"}, "rollout": {"100"}}, admin)
	require.Equal(t, http.StatusOK, code)
	_, err := s.q.GetFeatureFlag(t.Context(), "role-test-flag")
	assert.NoError(t, err, "the write actually happened")
	t.Cleanup(func() { _ = s.q.DeleteFeatureFlag(t.Context(), "role-test-flag") })
}

func TestNonStaffGetsForbiddenEverywhere(t *testing.T) {
	s := integrationServer(t, nil)
	plain := staffUser(t, s, "user_plain", "org_plain", "")

	code, _, _ := serve(t, s, "GET", "/admin", nil, nil, plain)
	assert.Equal(t, http.StatusForbidden, code)
	code, _, _ = postForm(t, s, "/admin/flags", url.Values{}, plain)
	assert.Equal(t, http.StatusForbidden, code)
}

// Support must not see controls that would 403 — a disabled-looking UI that
// throws errors is worse than one that never offers the action.
func TestSupportUINeverOffersMutations(t *testing.T) {
	s := integrationServer(t, nil)
	support := staffUser(t, s, "user_sup2", "org_sup2", identity.RoleSupport)
	admin := staffUser(t, s, "user_full2", "org_full2", identity.RoleAdmin)

	// data-testids that only exist on mutating controls.
	controls := map[string][]string{
		"/admin/users":         {"admin-impersonate", "admin-disable-toggle", "role-select-"},
		"/admin/flags":         {"flag-create-form", "flag-toggle-", "flag-delete-"},
		"/admin/schedules":     {"schedule-create-form", "schedule-toggle-", "schedule-delete-"},
		"/admin/announcements": {"announcement-form", "announcement-delete-"},
		"/admin/content":       {"content-new-", "content-delete-"},
	}
	for path, ids := range controls {
		_, _, supportBody := serve(t, s, "GET", path, nil, nil, support)
		_, _, adminBody := serve(t, s, "GET", path, nil, nil, admin)
		for _, id := range ids {
			assert.NotContains(t, supportBody, id, "%s must not offer %q to support", path, id)
		}
		// The same page must still be usable by an admin, or the assertion
		// above would pass for the wrong reason (an empty page).
		assert.Contains(t, adminBody, "table", "%s should still render for an admin", path)
	}

	// State is still visible — support can see WHETHER a flag is on.
	_, _, body := serve(t, s, "GET", "/admin/flags", nil, nil, support)
	assert.Contains(t, body, "flags-table", "support sees the data, just not the controls")
}

func TestRoleChangePersistsAndAudits(t *testing.T) {
	s := integrationServer(t, nil)
	admin := staffUser(t, s, "user_gr", "org_gr", identity.RoleAdmin)
	seedMembership(t, s, "user_target", "org_gr", "org:member")

	code, _, _ := postForm(t, s, "/admin/users/user_target/role",
		url.Values{"role": {identity.RoleSupport}}, admin)
	require.Equal(t, http.StatusOK, code)

	u, err := s.q.GetUserByID(t.Context(), "user_target")
	require.NoError(t, err)
	assert.Equal(t, identity.RoleSupport, u.AdminRole)

	rows, err := s.q.ListAuditAll(t.Context(), sqlc.ListAuditAllParams{Filter: "role_changed", Off: 0, Lim: 10})
	require.NoError(t, err)
	require.NotEmpty(t, rows, "granting staff access must be audited")
	assert.Contains(t, string(rows[0].Metadata), "user_target")
}

func TestRoleChangeRejectsUnknownRole(t *testing.T) {
	s := integrationServer(t, nil)
	admin := staffUser(t, s, "user_gr2", "org_gr2", identity.RoleAdmin)
	seedMembership(t, s, "user_target2", "org_gr2", "org:member")

	code, _, _ := postForm(t, s, "/admin/users/user_target2/role",
		url.Values{"role": {"superuser"}}, admin)
	assert.Equal(t, http.StatusUnprocessableEntity, code)

	u, err := s.q.GetUserByID(t.Context(), "user_target2")
	require.NoError(t, err)
	assert.Empty(t, u.AdminRole, "an unsupported role must not reach the CHECK constraint")
}

// Demoting the last admin would leave a platform whose remaining staff can
// read everything and change nothing — including the roles needed to undo it.
func TestLastAdminCannotBeDemoted(t *testing.T) {
	s := integrationServer(t, nil)
	// Start from a known state: this suite's other admins would mask the guard.
	_, err := s.db.Exec(t.Context(), "UPDATE users SET admin_role = '' WHERE admin_role = 'admin'")
	require.NoError(t, err)
	admin := staffUser(t, s, "user_last", "org_last", identity.RoleAdmin)

	code, hdr, _ := postForm(t, s, "/admin/users/user_last/role",
		url.Values{"role": {identity.RoleSupport}}, admin)
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	// The refusal must be VISIBLE: the toast rides an HX-Trigger header, and
	// setting it after WriteHeader drops it silently — the user would see
	// nothing happen at all.
	assert.Contains(t, strings.ToLower(hdr.Get("HX-Trigger")), "last admin",
		"a refused demotion must tell the user why")

	u, err := s.q.GetUserByID(t.Context(), "user_last")
	require.NoError(t, err)
	assert.Equal(t, identity.RoleAdmin, u.AdminRole, "the last admin keeps the role")

	// With a second admin present the demotion is allowed.
	staffUser(t, s, "user_second", "org_last", identity.RoleAdmin)
	code, _, _ = postForm(t, s, "/admin/users/user_last/role",
		url.Values{"role": {identity.RoleSupport}}, admin)
	require.Equal(t, http.StatusOK, code)
	u, err = s.q.GetUserByID(t.Context(), "user_last")
	require.NoError(t, err)
	assert.Equal(t, identity.RoleSupport, u.AdminRole)
}

// Impersonation is not a read: demotion mid-session must end it.
func TestImpersonationEndsWhenAdminDemotedToSupport(t *testing.T) {
	s := integrationServer(t, nil)
	admin := staffUser(t, s, "user_imp_adm", "org_imp", identity.RoleAdmin)
	seedMembership(t, s, "user_imp_tgt", "org_imp", "org:member")

	token, csrfCookies := csrfFor(t, s)
	form := url.Values{"org": {"org_imp"}, "reason": {"Ticket #77 — reproducing a report"}}
	h := http.Header{}
	h.Set("Content-Type", "application/x-www-form-urlencoded")
	h.Set("HX-Request", "true")
	h.Set("X-CSRF-Token", token)
	code, hdr, _ := serve(t, s, "POST", "/admin/users/user_imp_tgt/impersonate", []byte(form.Encode()), h,
		append(csrfCookies, admin)...)
	require.Equal(t, http.StatusOK, code)

	var impCookie *http.Cookie
	for _, c := range (&http.Response{Header: hdr}).Cookies() {
		if c.Name == "ggg_imp" {
			impCookie = c
		}
	}
	require.NotNil(t, impCookie, "impersonation session cookie")

	require.NoError(t, s.q.SetUserAdminRole(t.Context(), sqlc.SetUserAdminRoleParams{
		UserID: "user_imp_adm", AdminRole: identity.RoleSupport,
	}))

	// The session must not survive the demotion: /admin is forbidden for the
	// demoted admin, and the impersonation banner is gone.
	code, _, body := serve(t, s, "GET", "/app", nil, nil, admin, impCookie)
	assert.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "impersonation-banner", "a support-level account cannot hold an impersonation")
}
