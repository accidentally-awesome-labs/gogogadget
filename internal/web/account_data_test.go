package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"fmt"
	"github.com/gogogadget/gogogadget/internal/db"
	"reflect"
)

type exportShape struct {
	ExportedAt string `json:"exported_at"`
	User       struct {
		UserID string `json:"user_id"`
		Email  string `json:"email"`
	} `json:"user"`
	Memberships []struct {
		OrgID   string `json:"org_id"`
		OrgName string `json:"org_name"`
		Role    string `json:"role"`
	} `json:"memberships"`
	Notifications []map[string]any `json:"notifications"`
	Audit         []map[string]any `json:"audit"`
}

func auditRows(t *testing.T, q *sqlc.Queries, filter string) []sqlc.ListAuditAllRow {
	t.Helper()
	rows, err := q.ListAuditAll(t.Context(), sqlc.ListAuditAllParams{Filter: filter, Off: 0, Lim: 10})
	require.NoError(t, err)
	return rows
}

func TestAccountExportDownloadsJSON(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_ex", "org_ex", "org:admin")
	cookie := sessionCookie("user_ex", "org_ex", "org:admin")

	code, header, body := serve(t, s, "GET", "/app/settings/account/export", nil, nil, cookie)
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, header.Get("Content-Disposition"), `attachment; filename="gogogadget-data-export.json"`)
	assert.Contains(t, header.Get("Content-Type"), "application/json")

	var export exportShape
	require.NoError(t, json.Unmarshal([]byte(body), &export))
	assert.Equal(t, "user_ex", export.User.UserID)
	assert.Equal(t, "user_ex@example.com", export.User.Email)
	require.Len(t, export.Memberships, 1)
	assert.Equal(t, "org_ex", export.Memberships[0].OrgID)
	assert.Equal(t, "org:admin", export.Memberships[0].Role)
	assert.NotEmpty(t, export.ExportedAt)

	assert.Len(t, auditRows(t, s.q, "account.exported"), 1, "the export itself is audited")
}

func TestAccountDeleteSoleMemberOrgCascades(t *testing.T) {
	s := integrationServer(t, nil) // DevAuthBypass → DevDeleter no-op upstream
	seedMembership(t, s, "user_del", "org_del", "org:admin")
	cookie := sessionCookie("user_del", "org_del", "org:admin")

	// Wrong email → 422, nothing deleted.
	form := url.Values{"confirm_email": []string{"wrong@example.com"}}
	code, _, body := postForm(t, s, "/app/settings/account/delete", form, cookie)
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Contains(t, body, "match")
	_, err := s.q.GetUserByID(t.Context(), "user_del")
	require.NoError(t, err, "user survives a mismatched confirm")

	// Right email → account + sole-member org gone, cookie cleared, redirect home.
	form = url.Values{"confirm_email": []string{"user_del@example.com"}}
	code, header, _ := postForm(t, s, "/app/settings/account/delete", form, cookie)
	assert.Equal(t, http.StatusOK, code, "HX-Redirect rides a 200 (postForm sends HX-Request)")
	assert.Equal(t, "/", header.Get("HX-Redirect"), "hard navigation off the authed shell")
	var cleared bool
	for _, c := range header.Values("Set-Cookie") {
		if strings.HasPrefix(c, "__session=") && strings.Contains(c, "Max-Age=0") {
			cleared = true
		}
	}
	assert.True(t, cleared, "session cookie cleared")

	_, err = s.q.GetUserByID(t.Context(), "user_del")
	require.Error(t, err, "mirror row deleted")
	_, err = s.q.GetOrgByID(t.Context(), "org_del")
	require.Error(t, err, "sole-member org deleted with the account")

	// Audit rows deliberately survive (audit_log has no FKs by design).
	assert.Len(t, auditRows(t, s.q, "account.deleted"), 1)
}

func TestAccountDeleteBlockedWhenSoleAdminOfMultiMemberOrg(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_bl", "org_bl", "org:admin")
	seedMembership(t, s, "user_bl2", "org_bl", "org:member")
	cookie := sessionCookie("user_bl", "org_bl", "org:admin")

	form := url.Values{"confirm_email": []string{"user_bl@example.com"}}
	code, _, body := postForm(t, s, "/app/settings/account/delete", form, cookie)
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Contains(t, body, "org_bl", "blocker names the org")

	_, err := s.q.GetUserByID(t.Context(), "user_bl")
	require.NoError(t, err, "nothing deleted when blocked")
}

func TestAccountDeleteRefusedUnderImpersonation(t *testing.T) {
	s := integrationServer(t, nil)
	adminUser(t, s, "user_imp_admin", "org_imp_a")
	seedMembership(t, s, "user_imp_t", "org_imp_t", "org:admin")

	// Start a real impersonation; harvest BOTH cookies it issues.
	adminCookie := sessionCookie("user_imp_admin", "org_imp_a", "org:admin")
	code, header, _ := postForm(t, s, "/admin/users/user_imp_t/impersonate",
		url.Values{"reason": []string{"Ticket #101 — verifying deletion is refused"}}, adminCookie)
	require.Equal(t, http.StatusOK, code)
	var imp string
	for _, c := range header.Values("Set-Cookie") {
		switch {
		case strings.HasPrefix(c, "ggg_imp="):
			imp = strings.SplitN(strings.TrimPrefix(c, "ggg_imp="), ";", 2)[0]
		}
	}
	require.NotEmpty(t, imp, "impersonation issues its own cookie")

	// Real-world pairing: the admin's own session cookie + the impersonation
	// cookie; sessionLoad swaps the acting identity to the target.
	form := url.Values{"confirm_email": []string{"user_imp_t@example.com"}}
	code, _, _ = postForm(t, s, "/app/settings/account/delete", form,
		adminCookie,
		&http.Cookie{Name: "ggg_imp", Value: imp})
	assert.Equal(t, http.StatusForbidden, code, "never delete under impersonation")
	_, err := s.q.GetUserByID(t.Context(), "user_imp_t")
	require.NoError(t, err, "target user survives")
}

// The org export spans every member's activity, the whole audit trail, and
// the API/webhook inventory — an owner's view of the company, not something
// any member should walk out with.
func TestOrgExportRequiresOrgAdmin(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_oexp_m", "org_oexp", "org:member")

	code, _, _ := postForm(t, s, "/app/settings/org/export", url.Values{},
		sessionCookie("user_oexp_m", "org_oexp", "org:member"))
	assert.Equal(t, http.StatusForbidden, code)

	var jobs int
	require.NoError(t, s.db.QueryRow(t.Context(),
		`SELECT count(*) FROM jobs WHERE kind = 'export.org_json'`).Scan(&jobs))
	assert.Zero(t, jobs, "a refused export must not enqueue work")
}

func TestOrgExportEnqueuesAndAudits(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_oexp_a", "org_oexp2", "org:admin")
	t.Cleanup(func() {
		_, _ = s.db.Exec(t.Context(), "DELETE FROM jobs WHERE kind = 'export.org_json'")
	})

	code, _, _ := postForm(t, s, "/app/settings/org/export", url.Values{},
		sessionCookie("user_oexp_a", "org_oexp2", "org:admin"))
	require.Equal(t, http.StatusOK, code)

	var kind, payload string
	require.NoError(t, s.db.QueryRow(t.Context(),
		`SELECT kind, payload::text FROM jobs WHERE kind = 'export.org_json' ORDER BY id DESC LIMIT 1`).Scan(&kind, &payload))
	assert.Contains(t, payload, "org_oexp2", "the job carries the requesting org")
	assert.Contains(t, payload, "user_oexp_a", "…and who to notify")

	rows, err := s.q.ListAuditAll(t.Context(), sqlc.ListAuditAllParams{Filter: "org.exported", Off: 0, Lim: 5})
	require.NoError(t, err)
	assert.NotEmpty(t, rows, "exporting the whole organization is an auditable act")
}

// The button must not be offered to someone who would be refused.
func TestOrgExportCardOnlyForAdmins(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_oexp_v", "org_oexp3", "org:member")
	seedMembership(t, s, "user_oexp_va", "org_oexp3", "org:admin")

	_, _, memberBody := serve(t, s, "GET", "/app/settings/org", nil, nil,
		sessionCookie("user_oexp_v", "org_oexp3", "org:member"))
	assert.NotContains(t, memberBody, "org-export")

	_, _, adminBody := serve(t, s, "GET", "/app/settings/org", nil, nil,
		sessionCookie("user_oexp_va", "org_oexp3", "org:admin"))
	assert.Contains(t, adminBody, `data-testid="org-export"`)
}

// The account export must carry every user-scoped table a module declares
// exportable. Same shape as the org-export guard: the DTO choices are hand
// work, the coverage list is generated, and a new data module cannot be
// installed into silence.
func TestAccountExportCoversEveryDeclaredExportableTable(t *testing.T) {
	collections := map[string]bool{
		"users": true, "org_members": true, "notifications": true,
		"notification_preferences": true, "audit_log": true,
	}
	value := reflect.TypeOf(accountExport{})
	for _, field := range reflect.VisibleFields(value) {
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			collections[name] = true
		}
	}

	var missing []string
	for _, d := range db.DataLifecycleRegistry {
		if d.Scope != "user" || !d.Export {
			continue
		}
		if !collections[d.Table] {
			missing = append(missing, fmt.Sprintf("%s (declared by %s)", d.Table, d.Module))
		}
	}
	require.Empty(t, missing,
		"account export omits tables declared exportable: %v", missing)
}
