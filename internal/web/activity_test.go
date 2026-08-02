package web

import (
	"net/http"
	"testing"

	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActivityVisibilityScoping(t *testing.T) {
	s := integrationServer(t, nil)
	ctx := t.Context()
	seedUser(t, s, "user_act", "act@example.com", "Act")
	seedOrg(t, s, "org_act", "act")
	seedOrg(t, s, "org_act2", "act2")
	require.NoError(t, s.q.UpsertMembership(ctx, sqlc.UpsertMembershipParams{ClerkOrgID: "org_act", ClerkUserID: "user_act", Role: "org:admin"}))

	audit.Log(ctx, s.q, "org_act", "user_act", "project.created", map[string]any{"name": "Alpha"})
	audit.Log(ctx, s.q, "org_act", "user_act", "member.joined", map[string]any{"role": "org:admin"})
	audit.Log(ctx, s.q, "org_act2", "user_act", "project.deleted", map[string]any{"name": "Secret"})

	// Org member sees their own rows, including the actor's email…
	code, _, body := serve(t, s, "GET", "/app/activity", nil, nil, sessionCookie("user_act", "org_act", "org:admin"))
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "project.created")
	assert.Contains(t, body, "member.joined")
	assert.Contains(t, body, "act@example.com")
	// …and never another org's rows.
	assert.NotContains(t, body, "project.deleted")
}

func TestActivityPaginationFragment(t *testing.T) {
	s := integrationServer(t, nil)
	ctx := t.Context()
	seedUser(t, s, "user_pg", "pg@example.com", "Pg")
	seedOrg(t, s, "org_pg", "pg")
	require.NoError(t, s.q.UpsertMembership(ctx, sqlc.UpsertMembershipParams{ClerkOrgID: "org_pg", ClerkUserID: "user_pg", Role: "org:admin"}))

	for range 25 {
		audit.Log(ctx, s.q, "org_pg", "user_pg", "project.created", nil)
	}

	// Page 2 via htmx (non-boosted) → bare table fragment, no layout.
	h := http.Header{}
	h.Set("HX-Request", "true")
	code, _, body := serve(t, s, "GET", "/app/activity?page=2", nil, h, sessionCookie("user_pg", "org_pg", "org:admin"))
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "table-container")
	assert.Contains(t, body, "Page 2 of 2")
	assert.NotContains(t, body, `<html lang="en">`)

	// Boosted nav to the same URL → full layout.
	h.Set("HX-Boosted", "true")
	code, _, body = serve(t, s, "GET", "/app/activity?page=2", nil, h, sessionCookie("user_pg", "org_pg", "org:admin"))
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `<html lang="en">`)
}
