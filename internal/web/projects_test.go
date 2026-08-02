package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedMembership(t *testing.T, s *Server, userID, orgID, role string) {
	t.Helper()
	seedUser(t, s, userID, userID+"@example.com", userID)
	seedOrg(t, s, orgID, orgID)
	require.NoError(t, s.q.UpsertMembership(t.Context(), sqlc.UpsertMembershipParams{
		ClerkOrgID: orgID, ClerkUserID: userID, Role: role,
	}))
}

// postForm issues a CSRF-tokened form POST against the full stack.
func postForm(t *testing.T, s *Server, target string, form url.Values, cookies ...*http.Cookie) (int, http.Header, string) {
	t.Helper()
	token, csrfCookies := csrfFor(t, s)
	h := http.Header{}
	h.Set("Content-Type", "application/x-www-form-urlencoded")
	h.Set("HX-Request", "true")
	h.Set("X-CSRF-Token", token)
	all := append(append([]*http.Cookie{}, csrfCookies...), cookies...)
	return serve(t, s, "POST", target, []byte(form.Encode()), h, all...)
}

func TestProjectCrossOrg404(t *testing.T) {
	s := integrationServer(t, nil)
	ctx := t.Context()
	seedMembership(t, s, "user_x1", "org_x1", "org:admin")
	seedMembership(t, s, "user_x2", "org_x2", "org:admin")

	p, err := s.q.CreateProject(ctx, sqlc.CreateProjectParams{ClerkOrgID: "org_x2", Name: "Org B secret"})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = s.db.Exec(context.Background(), "DELETE FROM projects WHERE clerk_org_id IN ('org_x1','org_x2')")
	})

	base := fmt.Sprintf("/app/projects/%d", p.ID)
	// Org A cannot read, update, archive, or delete org B's project.
	code, _, _ := serve(t, s, "GET", base+"/edit", nil, nil, sessionCookie("user_x1", "org_x1", "org:admin"))
	assert.Equal(t, http.StatusNotFound, code)

	code, _, _ = postForm(t, s, base, url.Values{"name": {"hijacked"}}, sessionCookie("user_x1", "org_x1", "org:admin"))
	assert.Equal(t, http.StatusNotFound, code)

	code, _, _ = postForm(t, s, base+"/archive", url.Values{}, sessionCookie("user_x1", "org_x1", "org:admin"))
	assert.Equal(t, http.StatusNotFound, code)

	h := http.Header{}
	h.Set("HX-Request", "true")
	token, csrfCookies := csrfFor(t, s)
	h.Set("X-CSRF-Token", token)
	code, _, _ = serve(t, s, "DELETE", base, nil, h, append(csrfCookies, sessionCookie("user_x1", "org_x1", "org:admin"))...)
	assert.Equal(t, http.StatusNotFound, code)

	// And it is untouched.
	got, err := s.q.GetProjectByID(ctx, sqlc.GetProjectByIDParams{ID: p.ID, ClerkOrgID: "org_x2"})
	require.NoError(t, err)
	assert.Equal(t, "Org B secret", got.Name)
}

func TestFreePlanLimit422(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_l1", "org_l1", "org:admin")
	ctx := t.Context()
	for i := range 3 {
		_, err := s.q.CreateProject(ctx, sqlc.CreateProjectParams{ClerkOrgID: "org_l1", Name: fmt.Sprintf("P%d", i)})
		require.NoError(t, err)
	}
	t.Cleanup(func() { _, _ = s.db.Exec(context.Background(), "DELETE FROM projects WHERE clerk_org_id = 'org_l1'") })

	// 4th create on the free plan → 422 fragment with upgrade CTA.
	code, _, body := postForm(t, s, "/app/projects", url.Values{"name": {"One too many"}}, sessionCookie("user_l1", "org_l1", "org:admin"))
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Contains(t, body, "plan-limit")
	assert.Contains(t, body, "Upgrade")
}

func TestProjectValidation422(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_v1", "org_v1", "org:admin")

	// Empty name → 422 with the field error.
	code, _, body := postForm(t, s, "/app/projects", url.Values{"name": {"  "}}, sessionCookie("user_v1", "org_v1", "org:admin"))
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Contains(t, body, "form-error")
	assert.Contains(t, body, "Name is required")

	// 81 chars → 422.
	code, _, body = postForm(t, s, "/app/projects", url.Values{"name": {strings.Repeat("x", 81)}}, sessionCookie("user_v1", "org_v1", "org:admin"))
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Contains(t, body, "80 characters")
}

func TestProjectCreateSearchArchiveDelete(t *testing.T) {
	s := integrationServer(t, nil)
	ctx := t.Context()
	seedMembership(t, s, "user_c1", "org_c1", "org:admin")
	cookie := sessionCookie("user_c1", "org_c1", "org:admin")
	t.Cleanup(func() {
		_, _ = s.db.Exec(context.Background(), "DELETE FROM projects WHERE clerk_org_id = 'org_c1'")
		_, _ = s.db.Exec(context.Background(), "DELETE FROM audit_log WHERE clerk_org_id = 'org_c1'")
	})

	// Create → HX-Redirect + toast.
	code, hdr, _ := postForm(t, s, "/app/projects", url.Values{"name": {"Launch checklist"}}, cookie)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "/app/projects", hdr.Get("HX-Redirect"))
	assert.Contains(t, hdr.Get("HX-Trigger"), "Project created")

	// Audit row written.
	var n int
	require.NoError(t, s.db.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE clerk_org_id='org_c1' AND action='project.created'`).Scan(&n))
	assert.Equal(t, 1, n)

	// Search finds it; nonsense search doesn't.
	code, _, body := serve(t, s, "GET", "/app/projects?q=launch", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "Launch checklist")
	code, _, body = serve(t, s, "GET", "/app/projects?q=zzzz", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "Launch checklist")

	// Archive → disappears from the default list.
	var id int64
	require.NoError(t, s.db.QueryRow(ctx, `SELECT id FROM projects WHERE clerk_org_id='org_c1'`).Scan(&id))
	code, _, _ = postForm(t, s, fmt.Sprintf("/app/projects/%d/archive", id), url.Values{}, cookie)
	require.Equal(t, http.StatusOK, code)
	code, _, body = serve(t, s, "GET", "/app/projects", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "Launch checklist")

	// Recreate + delete → 200 empty, audit written.
	_, err := s.q.CreateProject(ctx, sqlc.CreateProjectParams{ClerkOrgID: "org_c1", Name: "Doomed"})
	require.NoError(t, err)
	require.NoError(t, s.db.QueryRow(ctx, `SELECT id FROM projects WHERE clerk_org_id='org_c1' AND status='active'`).Scan(&id))
	h := http.Header{}
	h.Set("HX-Request", "true")
	token, csrfCookies := csrfFor(t, s)
	h.Set("X-CSRF-Token", token)
	code, hdr, body = serve(t, s, "DELETE", fmt.Sprintf("/app/projects/%d", id), nil, h, append(csrfCookies, cookie)...)
	require.Equal(t, http.StatusOK, code)
	assert.Empty(t, body)
	assert.Contains(t, hdr.Get("HX-Trigger"), "Project deleted")
}

func TestDashboardRenders(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_dash", "org_dash", "org:admin")
	code, _, body := serve(t, s, "GET", "/app", nil, nil, sessionCookie("user_dash", "org_dash", "org:admin"))
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "stat-card")
	assert.Contains(t, body, "Getting started")
	assert.Contains(t, body, "Free") // plan name
}

var csrfTokenRe = regexp.MustCompile(`X-CSRF-Token(?:"|&#34;):\s*(?:"|&#34;)([^"&]+)`)

// csrfFor returns a usable masked nosurf token + its cookie from ONE page
// render — the pair belongs to the same token family.
func csrfFor(t *testing.T, s *Server) (string, []*http.Cookie) {
	t.Helper()
	req := httptest.NewRequest("GET", "/pricing", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	m := csrfTokenRe.FindStringSubmatch(rec.Body.String())
	require.Len(t, m, 2, "page must carry a CSRF token in hx-headers")
	return m[1], rec.Result().Cookies()
}
