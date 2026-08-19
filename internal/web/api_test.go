package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sqlcCreateP(orgID, name string) sqlc.CreateProjectParams {
	return sqlc.CreateProjectParams{ClerkOrgID: orgID, Name: name}
}

// createTokenViaUI drives the real create handler and returns the plaintext.
func createTokenViaUI(t *testing.T, s *Server, name, scope string, cookies ...*http.Cookie) string {
	t.Helper()
	code, _, body := postForm(t, s, "/app/settings/api/tokens",
		url.Values{"name": {name}, "scope": {scope}}, cookies...)
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, body, "api-token-reveal")
	start := strings.Index(body, "ggg_")
	require.NotEqual(t, -1, start, "reveal must contain the plaintext token")
	end := strings.Index(body[start:], "<")
	require.NotEqual(t, -1, end)
	return body[start : start+end]
}

func apiGet(t *testing.T, s *Server, target, token string) (int, map[string]any) {
	t.Helper()
	h := http.Header{}
	if token != "" {
		h.Set("Authorization", "Bearer "+token)
	}
	code, _, body := serve(t, s, "GET", target, nil, h)
	var out map[string]any
	if body != "" {
		require.NoError(t, json.Unmarshal([]byte(body), &out))
	}
	return code, out
}

func TestAPITokenLifecycle(t *testing.T) {
	s := integrationServer(t, nil)
	ctx := t.Context()
	seedMembership(t, s, "user_api", "org_api", "org:admin")
	seedMembership(t, s, "user_api2", "org_api2", "org:admin")

	_, err := s.q.CreateProject(ctx, sqlcCreateP("org_api", "Mine"))
	require.NoError(t, err)
	_, err = s.q.CreateProject(ctx, sqlcCreateP("org_api2", "NotYours"))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = s.db.Exec(context.Background(), "DELETE FROM projects WHERE clerk_org_id IN ('org_api','org_api2')")
	})

	token := createTokenViaUI(t, s, "ci", "read", sessionCookie("user_api", "org_api", "org:admin"))
	assert.True(t, strings.HasPrefix(token, "ggg_"), "token format")

	// Valid token → 200 with ONLY that org's rows.
	code, out := apiGet(t, s, "/api/v1/projects", token)
	require.Equal(t, http.StatusOK, code)
	projects := out["projects"].([]any)
	require.Len(t, projects, 1)
	assert.Equal(t, "Mine", projects[0].(map[string]any)["name"])

	// No header → 401 JSON.
	code, out = apiGet(t, s, "/api/v1/projects", "")
	require.Equal(t, http.StatusUnauthorized, code)
	assert.Equal(t, "unauthorized", out["error"].(map[string]any)["code"])

	// Revoked → 401.
	toks, err := s.q.ListAPITokensByOrg(ctx, "org_api")
	require.NoError(t, err)
	require.Len(t, toks, 1)
	require.NoError(t, s.q.RevokeAPIToken(ctx, sqlc.RevokeAPITokenParams{ID: toks[0].ID, ClerkOrgID: "org_api"}))
	code, out = apiGet(t, s, "/api/v1/projects", token)
	require.Equal(t, http.StatusUnauthorized, code)
}

func TestAPIScopeEnforcement(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_sc", "org_sc", "org:admin")

	readToken := createTokenViaUI(t, s, "reader", "read", sessionCookie("user_sc", "org_sc", "org:admin"))

	// read-scoped token POSTing → 403 JSON.
	h := http.Header{}
	h.Set("Authorization", "Bearer "+readToken)
	h.Set("Content-Type", "application/json")
	code, _, body := serve(t, s, "POST", "/api/v1/projects", []byte(`{"name":"x"}`), h)
	require.Equal(t, http.StatusForbidden, code)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &out))
	assert.Equal(t, "forbidden", out["error"].(map[string]any)["code"])
}

func TestAPIPlanLimit402(t *testing.T) {
	s := integrationServer(t, nil)
	ctx := t.Context()
	seedMembership(t, s, "user_lim", "org_lim", "org:admin")
	for i := range 3 {
		_, err := s.q.CreateProject(ctx, sqlcCreateP("org_lim", fmt.Sprintf("p%d", i)))
		require.NoError(t, err)
	}
	t.Cleanup(func() { _, _ = s.db.Exec(context.Background(), "DELETE FROM projects WHERE clerk_org_id='org_lim'") })

	writeToken := createTokenViaUI(t, s, "writer", "write", sessionCookie("user_lim", "org_lim", "org:admin"))
	h := http.Header{}
	h.Set("Authorization", "Bearer "+writeToken)
	h.Set("Content-Type", "application/json")
	code, _, body := serve(t, s, "POST", "/api/v1/projects", []byte(`{"name":"over"}`), h)
	require.Equal(t, http.StatusPaymentRequired, code)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &out))
	assert.Equal(t, "plan_limit", out["error"].(map[string]any)["code"])
}

func TestAPIWriteAndValidation(t *testing.T) {
	s := integrationServer(t, nil)
	ctx := t.Context()
	seedMembership(t, s, "user_wr", "org_wr", "org:admin")
	t.Cleanup(func() {
		_, _ = s.db.Exec(context.Background(), "DELETE FROM projects WHERE clerk_org_id='org_wr'")
		_, _ = s.db.Exec(context.Background(), "DELETE FROM audit_log WHERE clerk_org_id='org_wr'")
	})

	writeToken := createTokenViaUI(t, s, "writer", "write", sessionCookie("user_wr", "org_wr", "org:admin"))
	h := http.Header{}
	h.Set("Authorization", "Bearer "+writeToken)
	h.Set("Content-Type", "application/json")

	// Valid create → 201 + audit via api.
	code, _, body := serve(t, s, "POST", "/api/v1/projects", []byte(`{"name":"From API"}`), h)
	require.Equal(t, http.StatusCreated, code)
	assert.Contains(t, body, "From API")
	var via string
	require.NoError(t, s.db.QueryRow(ctx, `SELECT metadata->>'via' FROM audit_log WHERE clerk_org_id='org_wr'`).Scan(&via))
	assert.Equal(t, "api", via)

	// Invalid name → 422 validation_error.
	code, _, body = serve(t, s, "POST", "/api/v1/projects", []byte(`{"name":"  "}`), h)
	require.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Contains(t, body, "validation_error")

	// Bad JSON → 400.
	code, _, _ = serve(t, s, "POST", "/api/v1/projects", []byte(`{nope`), h)
	require.Equal(t, http.StatusBadRequest, code)
}

func TestAPIUnknownRoute404JSON(t *testing.T) {
	s := integrationServer(t, nil)
	code, out := apiGet(t, s, "/api/v1/nope", "")
	require.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "not_found", out["error"].(map[string]any)["code"])
}

// Cursor pagination's whole point is stability under concurrent writes.
// Offset paging shifts every row one position when a newer row is inserted,
// so a client walking pages sees the boundary row twice; a keyset cursor
// names a row, so an insert at the head is simply not in the walk.
func TestAPIProjectsCursorStableUnderInserts(t *testing.T) {
	s := integrationServer(t, nil)
	ctx := t.Context()
	seedMembership(t, s, "user_cur", "org_cur", "org:admin")
	t.Cleanup(func() {
		_, _ = s.db.Exec(context.Background(), "DELETE FROM projects WHERE clerk_org_id = 'org_cur'")
	})
	for i := range 6 {
		_, err := s.q.CreateProject(ctx, sqlcCreateP("org_cur", fmt.Sprintf("P%d", i)))
		require.NoError(t, err)
	}
	token := seedAPIToken(t, s, "org_cur", "read")

	names := func(m map[string]any) []string {
		rows, _ := m["projects"].([]any)
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.(map[string]any)["name"].(string))
		}
		return out
	}

	// Page 1.
	code, page1 := apiGet(t, s, "/api/v1/projects?limit=2", token)
	require.Equal(t, http.StatusOK, code)
	require.Len(t, names(page1), 2)
	next, ok := page1["next_cursor"].(string)
	require.True(t, ok, "a further page must advertise a cursor")

	// A write lands between page fetches — the classic offset-paging hazard.
	_, err := s.q.CreateProject(ctx, sqlcCreateP("org_cur", "Interloper"))
	require.NoError(t, err)

	code, page2 := apiGet(t, s, "/api/v1/projects?limit=2&cursor="+url.QueryEscape(next), token)
	require.Equal(t, http.StatusOK, code)

	seen := append(names(page1), names(page2)...)
	assert.NotContains(t, names(page2), "Interloper", "a row created after the walk started must not appear mid-walk")
	uniq := map[string]bool{}
	for _, n := range seen {
		assert.False(t, uniq[n], "cursor paging repeated row %q", n)
		uniq[n] = true
	}

	// Contrast: the same walk with offset does repeat the boundary row.
	_, off2 := apiGet(t, s, "/api/v1/projects?limit=2&offset=2", token)
	assert.Contains(t, names(page1), names(off2)[0],
		"offset paging repeats a row after an insert — this is why cursors exist")
}

func TestAPIProjectsCursorWalksEveryRowExactlyOnce(t *testing.T) {
	s := integrationServer(t, nil)
	ctx := t.Context()
	seedMembership(t, s, "user_curw", "org_curw", "org:admin")
	t.Cleanup(func() {
		_, _ = s.db.Exec(context.Background(), "DELETE FROM projects WHERE clerk_org_id = 'org_curw'")
	})
	const total = 7
	for i := range total {
		_, err := s.q.CreateProject(ctx, sqlcCreateP("org_curw", fmt.Sprintf("W%d", i)))
		require.NoError(t, err)
	}
	token := seedAPIToken(t, s, "org_curw", "read")

	seen, target, pages := map[string]bool{}, "/api/v1/projects?limit=3", 0
	for {
		pages++
		require.Less(t, pages, 10, "cursor walk must terminate")
		code, page := apiGet(t, s, target, token)
		require.Equal(t, http.StatusOK, code)
		for _, r := range page["projects"].([]any) {
			name := r.(map[string]any)["name"].(string)
			require.False(t, seen[name], "row %q returned twice", name)
			seen[name] = true
		}
		next, ok := page["next_cursor"].(string)
		if !ok {
			assert.Nil(t, page["next_cursor"], "last page reports next_cursor: null, not an empty string")
			break
		}
		target = "/api/v1/projects?limit=3&cursor=" + url.QueryEscape(next)
	}
	assert.Len(t, seen, total, "every row visited exactly once")
	assert.Equal(t, 3, pages, "7 rows at limit 3 = 3 pages, the last one short")
}

func TestAPIProjectsRejectsMalformedCursor(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_curb", "org_curb", "org:admin")
	token := seedAPIToken(t, s, "org_curb", "read")

	code, body := apiGet(t, s, "/api/v1/projects?cursor=not-a-cursor%21", token)
	assert.Equal(t, http.StatusBadRequest, code, "a bad cursor must not silently restart at page one")
	assert.Equal(t, "invalid_cursor", body["error"].(map[string]any)["code"])
}

// The per-token budget exists so one noisy token cannot spend another
// customer's allowance — that isolation is the property under test, not the
// arithmetic of the bucket (covered in internal/ratelimit).
func TestAPIPerTokenRateLimitIsolatesTokens(t *testing.T) {
	s := integrationServer(t, func(d *Deps) { d.Config.APIRateLimitPerMinute = 1 }) // burst 2
	seedMembership(t, s, "user_rl", "org_rl", "org:admin")
	noisy := seedAPIToken(t, s, "org_rl", "read")
	quiet := seedAPIToken(t, s, "org_rl", "read")

	var last int
	for range 3 {
		last, _ = apiGet(t, s, "/api/v1/projects", noisy)
	}
	require.Equal(t, http.StatusTooManyRequests, last, "the noisy token must exhaust its own budget")

	code, body := apiGet(t, s, "/api/v1/projects", noisy)
	assert.Equal(t, http.StatusTooManyRequests, code)
	assert.Equal(t, "rate_limited", body["error"].(map[string]any)["code"])

	code, _ = apiGet(t, s, "/api/v1/projects", quiet)
	assert.Equal(t, http.StatusOK, code, "a second token keeps its full budget — no shared bucket")
}

func TestAPIRateLimitSendsRetryAfterAndJSON(t *testing.T) {
	s := integrationServer(t, func(d *Deps) { d.Config.APIRateLimitPerMinute = 1 })
	seedMembership(t, s, "user_rl2", "org_rl2", "org:admin")
	token := seedAPIToken(t, s, "org_rl2", "read")

	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	var code int
	var header http.Header
	var body string
	for range 3 {
		code, header, body = serve(t, s, "GET", "/api/v1/projects", nil, h)
	}
	require.Equal(t, http.StatusTooManyRequests, code)
	assert.Equal(t, "1", header.Get("Retry-After"), "clients need to know when to come back")
	assert.Contains(t, header.Get("Content-Type"), "application/json", "an API 429 must not be the HTML error page")
	assert.Contains(t, body, "rate_limited")
}

// A rejected credential must fail as unauthorized, never as rate-limited:
// budget is spent by authenticated identity, so a 429 always means "over
// budget" rather than leaking that some other token is busy.
func TestAPIRateLimitDoesNotApplyBeforeAuth(t *testing.T) {
	s := integrationServer(t, func(d *Deps) { d.Config.APIRateLimitPerMinute = 1 })
	for range 5 {
		code, body := apiGet(t, s, "/api/v1/projects", "ggg_nope")
		require.Equal(t, http.StatusUnauthorized, code)
		assert.Equal(t, "unauthorized", body["error"].(map[string]any)["code"])
	}
}
