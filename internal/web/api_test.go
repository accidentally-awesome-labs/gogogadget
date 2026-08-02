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
