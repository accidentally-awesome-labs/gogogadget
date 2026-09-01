package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gogogadget/gogogadget/internal/api"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The whole point of an idempotency key is that a client which cannot tell
// whether its POST landed may retry without creating a second row. Every test
// here asserts that outcome (row counts, replayed bodies), not the mechanism.

func idemPost(t *testing.T, s *Server, token, key, body string) (int, http.Header, string) {
	t.Helper()
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	h.Set("Content-Type", "application/json")
	if key != "" {
		h.Set("Idempotency-Key", key)
	}
	return serve(t, s, "POST", "/api/v1/projects", []byte(body), h)
}

func projectCount(t *testing.T, s *Server, org string) int {
	t.Helper()
	var n int
	require.NoError(t, s.db.QueryRow(context.Background(),
		`SELECT count(*) FROM projects WHERE org_id = $1`, org).Scan(&n))
	return n
}

func idemCleanup(t *testing.T, s *Server, org string) {
	t.Cleanup(func() {
		_, _ = s.db.Exec(context.Background(), "DELETE FROM projects WHERE org_id=$1", org)
		_, _ = s.db.Exec(context.Background(), "DELETE FROM idempotency_keys WHERE org_id=$1", org)
		_, _ = s.db.Exec(context.Background(), "DELETE FROM audit_log WHERE org_id=$1", org)
	})
}

func TestIdempotentRetryReplaysAndCreatesOnce(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_idem", "org_idem", "org:admin")
	idemCleanup(t, s, "org_idem")
	token := seedAPIToken(t, s, "org_idem", "write")

	code, hdr, first := idemPost(t, s, token, "key-1", `{"name":"Only once"}`)
	require.Equal(t, http.StatusCreated, code)
	assert.Empty(t, hdr.Get("Idempotency-Replayed"), "the original request is not a replay")

	code, hdr, second := idemPost(t, s, token, "key-1", `{"name":"Only once"}`)
	assert.Equal(t, http.StatusCreated, code, "the retry reports the original outcome, not a fresh 201 for new work")
	assert.Equal(t, "true", hdr.Get("Idempotency-Replayed"), "clients need to distinguish a replay from a new effect")
	assert.Equal(t, first, second, "the replay is the stored response, byte for byte — not a re-serialization")

	assert.Equal(t, 1, projectCount(t, s, "org_idem"), "the retry must not have created a second project")
}

func TestIdempotentKeyReuseWithDifferentBodyConflicts(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_idc", "org_idc", "org:admin")
	idemCleanup(t, s, "org_idc")
	token := seedAPIToken(t, s, "org_idc", "write")

	code, _, _ := idemPost(t, s, token, "key-2", `{"name":"First"}`)
	require.Equal(t, http.StatusCreated, code)

	code, _, body := idemPost(t, s, token, "key-2", `{"name":"Different"}`)
	assert.Equal(t, http.StatusConflict, code,
		"serving the first response for a different request would be a silently wrong answer")
	var out map[string]map[string]string
	require.NoError(t, json.Unmarshal([]byte(body), &out))
	assert.Equal(t, "idempotency_conflict", out["error"]["code"])
	assert.Equal(t, 1, projectCount(t, s, "org_idc"), "the conflicting request must not execute")
}

// A key belongs to one operation. Reusing it on another endpoint is the same
// mistake as reusing it with another body, and gets the same refusal.
func TestIdempotentKeyIsScopedToEndpoint(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_ide", "org_ide", "org:admin")
	idemCleanup(t, s, "org_ide")
	token := seedAPIToken(t, s, "org_ide", "write")

	code, _, _ := idemPost(t, s, token, "key-3", `{"name":"P"}`)
	require.Equal(t, http.StatusCreated, code)

	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	h.Set("Content-Type", "application/json")
	h.Set("Idempotency-Key", "key-3")
	code, _, body := serve(t, s, "POST", "/api/v1/ai/chat", []byte(`{"name":"P"}`), h)
	assert.Equal(t, http.StatusConflict, code)
	assert.Contains(t, body, "idempotency_conflict")
}

func TestIdempotentIsOptIn(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_ido", "org_ido", "org:admin")
	idemCleanup(t, s, "org_ido")
	token := seedAPIToken(t, s, "org_ido", "write")

	for range 2 {
		code, hdr, _ := idemPost(t, s, token, "", `{"name":"Dup"}`)
		require.Equal(t, http.StatusCreated, code)
		assert.Empty(t, hdr.Get("Idempotency-Replayed"))
	}
	assert.Equal(t, 2, projectCount(t, s, "org_ido"),
		"without a key the API keeps its old behaviour: two POSTs are two creates")

	var keys int
	require.NoError(t, s.db.QueryRow(context.Background(),
		`SELECT count(*) FROM idempotency_keys WHERE org_id='org_ido'`).Scan(&keys))
	assert.Zero(t, keys, "keyless requests must not write rows")
}

// A validation failure is a real outcome: replaying it is correct, and it
// must not have consumed the key in a way that hides the error.
func TestIdempotentStoresClientErrors(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_idv", "org_idv", "org:admin")
	idemCleanup(t, s, "org_idv")
	token := seedAPIToken(t, s, "org_idv", "write")

	code, _, first := idemPost(t, s, token, "key-4", `{"name":"   "}`)
	require.Equal(t, http.StatusUnprocessableEntity, code)

	code, hdr, second := idemPost(t, s, token, "key-4", `{"name":"   "}`)
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Equal(t, "true", hdr.Get("Idempotency-Replayed"))
	assert.Equal(t, first, second, "a replayed client error is byte-identical too")
}

func TestIdempotentRejectsOverlongKey(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_idl", "org_idl", "org:admin")
	idemCleanup(t, s, "org_idl")
	token := seedAPIToken(t, s, "org_idl", "write")

	long := make([]byte, 256)
	for i := range long {
		long[i] = 'k'
	}
	code, _, body := idemPost(t, s, token, string(long), `{"name":"X"}`)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body, "invalid_idempotency_key")
	assert.Zero(t, projectCount(t, s, "org_idl"))
}

// Keys are org-scoped, not token-scoped: a client that rotates credentials
// mid-retry (exactly when retries happen) must still deduplicate.
func TestIdempotentKeyIsOrgScopedNotTokenScoped(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_ids", "org_ids", "org:admin")
	idemCleanup(t, s, "org_ids")
	first := seedAPIToken(t, s, "org_ids", "write")
	rotated := seedAPIToken(t, s, "org_ids", "write")

	code, _, _ := idemPost(t, s, first, "key-5", `{"name":"Once"}`)
	require.Equal(t, http.StatusCreated, code)

	code, hdr, _ := idemPost(t, s, rotated, "key-5", `{"name":"Once"}`)
	assert.Equal(t, http.StatusCreated, code)
	assert.Equal(t, "true", hdr.Get("Idempotency-Replayed"))
	assert.Equal(t, 1, projectCount(t, s, "org_ids"))
}

// Two identical requests racing (the real retry shape: client times out and
// resends while the first is still running) must produce exactly one project.
func TestIdempotentConcurrentRetriesCreateOnce(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_idr", "org_idr", "org:admin")
	idemCleanup(t, s, "org_idr")
	token := seedAPIToken(t, s, "org_idr", "write")

	const racers = 6
	codes := make([]int, racers)
	var wg sync.WaitGroup
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i], _, _ = idemPost(t, s, token, "key-race", `{"name":"Racer"}`)
		}()
	}
	wg.Wait()

	created, conflicts := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("unexpected status %d — a race must resolve to created or in-progress", c)
		}
	}
	assert.GreaterOrEqual(t, created, 1, "someone must win the race")
	assert.Equal(t, racers, created+conflicts)
	assert.Equal(t, 1, projectCount(t, s, "org_idr"), "the PK conflict is the lock: exactly one execution")
}

// A 5xx says nothing about whether the work should happen, so the claim is
// released — otherwise a transient failure would poison the key for its whole
// retention window, which is the exact situation the client is retrying out of.
func TestIdempotentReleasesClaimOnServerError(t *testing.T) {
	s := integrationServer(t, nil)
	ctx := t.Context()
	seedMembership(t, s, "user_id5", "org_id5", "org:admin")
	idemCleanup(t, s, "org_id5")

	mw := &api.Middleware{Q: s.q}
	boom := mw.Idempotent(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.WriteError(w, http.StatusInternalServerError, "internal_error", "boom")
	}))
	req, rec := idemRequest(t, s, "org_id5", "key-5xx", `{"name":"X"}`)
	boom.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)

	_, err := s.q.GetIdempotencyKey(ctx, sqlc.GetIdempotencyKeyParams{OrgID: "org_id5", Key: "key-5xx"})
	assert.Error(t, err, "the claim must be gone so the client can genuinely retry")
}

// idemRequest builds a request already carrying the authenticated org, so a
// bare Idempotent wrapper can be exercised without the token middleware.
func idemRequest(t *testing.T, s *Server, orgID, key, body string) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	org, err := s.q.GetOrgByID(t.Context(), orgID)
	require.NoError(t, err)
	req := httptest.NewRequest("POST", "/api/v1/projects", strings.NewReader(body))
	req.Header.Set("Idempotency-Key", key)
	req = req.WithContext(identity.WithOrg(t.Context(), &org))
	return req, httptest.NewRecorder()
}
