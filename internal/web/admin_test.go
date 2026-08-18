package web

import (
	"net/http"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminNonAdmin403(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_na", "org_na", "org:admin")

	code, _, body := serve(t, s, "GET", "/admin", nil, nil, sessionCookie("user_na", "org_na", "org:admin"))
	assert.Equal(t, http.StatusForbidden, code)
	assert.Contains(t, body, "You don&#39;t have access", "dedicated 403 page")
}

func adminUser(t *testing.T, s *Server, id, orgID string) {
	t.Helper()
	seedMembership(t, s, id, orgID, "org:admin")
	require.NoError(t, s.q.SetUserAdminByEmail(t.Context(), sqlc.SetUserAdminByEmailParams{Email: id + "@example.com", IsAdmin: true}))
}

func TestAdminHomeStats(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_root", "org_root")

	code, _, body := serve(t, s, "GET", "/admin", nil, nil, sessionCookie("user_root", "org_root", "org:admin"))
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "Total users")
	assert.Contains(t, body, "MRR")
	assert.Contains(t, body, "Signups, last 30 days")
}

func TestAdminDisableFlow(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_root2", "org_root2")
	seedMembership(t, s, "user_target", "org_target", "org:admin")
	targetCookie := sessionCookie("user_target", "org_target", "org:admin")

	// Target can use the app.
	code, _, _ := serve(t, s, "GET", "/app", nil, nil, targetCookie)
	require.Equal(t, http.StatusOK, code)

	// Admin disables them.
	code, _, _ = postForm(t, s, "/admin/users/user_target/disable", nil, sessionCookie("user_root2", "org_root2", "org:admin"))
	require.Equal(t, http.StatusOK, code)

	u, err := s.q.GetUserByClerkID(t.Context(), "user_target")
	require.NoError(t, err)
	require.True(t, u.DisabledAt.Valid)

	// Next request from the disabled user → 403 disabled page.
	code, _, body := serve(t, s, "GET", "/app", nil, nil, targetCookie)
	require.Equal(t, http.StatusForbidden, code)
	assert.Contains(t, body, "Account disabled")

	// Audit row exists.
	var action string
	require.NoError(t, s.db.QueryRow(t.Context(), `SELECT action FROM audit_log WHERE action LIKE 'admin.user_%' ORDER BY id DESC LIMIT 1`).Scan(&action))
	assert.Equal(t, "admin.user_disabled", action)

	// Re-enable → access restored.
	code, _, _ = postForm(t, s, "/admin/users/user_target/disable", nil, sessionCookie("user_root2", "org_root2", "org:admin"))
	require.Equal(t, http.StatusOK, code)
	code, _, _ = serve(t, s, "GET", "/app", nil, nil, targetCookie)
	require.Equal(t, http.StatusOK, code)
}

func TestAdminOrgsPlanBadges(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_root3", "org_root3")

	// Give root3's org a pro subscription so the badge shows.
	_, err := s.q.UpsertSubscription(t.Context(), sqlc.UpsertSubscriptionParams{
		ClerkOrgID: "org_root3", PolarSubscriptionID: pgtype.Text{String: "sub_root3", Valid: true},
		PolarCustomerID: "cust_root3", ProductKey: "pro", Status: "active",
	})
	require.NoError(t, err)

	code, _, body := serve(t, s, "GET", "/admin/orgs", nil, nil, sessionCookie("user_root3", "org_root3", "org:admin"))
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "plan-badge")
	assert.Contains(t, body, "pro")
}

func TestAdminUsersSearch(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_root4", "org_root4")

	h := http.Header{}
	h.Set("HX-Request", "true")
	code, _, body := serve(t, s, "GET", "/admin/users?q=root4", nil, h, sessionCookie("user_root4", "org_root4", "org:admin"))
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "user_root4@example.com")
	assert.NotContains(t, body, `<html lang="en">`, "search must return the table fragment")
	assert.Contains(t, body, "admin-disable-toggle")
}
