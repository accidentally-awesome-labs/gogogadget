package flagsmith

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/cache"
	"github.com/gogogadget/gogogadget/internal/flags"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capturedRequest is one request the fake provider saw, in the shape
// internal/billing/polar/client_test.go records them.
type capturedRequest struct {
	method, path, query string
	environmentKey      string
	authorization       string
	contentType         string
	body                []byte
}

func (c capturedRequest) line() string {
	if c.query == "" {
		return c.method + " " + c.path
	}
	return c.method + " " + c.path + "?" + c.query
}

// route answers one method+path with a fixed status and body.
type route struct {
	method, path string
	status       int
	body         string
}

// fake is an httptest provider that serves both Flagsmith APIs from one host,
// which is also how a self-hosted deployment serves them.
type fake struct {
	t      *testing.T
	mu     sync.Mutex
	seen   []capturedRequest
	routes []route
	// unmatched records a request no route answered, so a wrong path fails
	// the assertion instead of silently returning 200.
	unmatched []string
}

func newFake(t *testing.T, routes ...route) (*Service, *fake) {
	t.Helper()
	f := &fake{t: t, routes: routes}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	service := New(Service{
		Flags:          srv.URL,
		Management:     srv.URL,
		EnvironmentKey: "ser.env_key_test",
		Token:          "tok_management_test",
		ProjectID:      7,
		EnvironmentID:  42,
		Cache:          newTestCache(),
	})
	return service, f
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	captured := capturedRequest{
		method:         r.Method,
		path:           r.URL.Path,
		query:          r.URL.RawQuery,
		environmentKey: r.Header.Get("X-Environment-Key"),
		authorization:  r.Header.Get("Authorization"),
		contentType:    r.Header.Get("Content-Type"),
		body:           body,
	}
	f.mu.Lock()
	f.seen = append(f.seen, captured)
	var matched *route
	for i := range f.routes {
		if f.routes[i].method == r.Method && f.routes[i].path == r.URL.Path {
			matched = &f.routes[i]
			break
		}
	}
	if matched == nil {
		f.unmatched = append(f.unmatched, captured.line())
	}
	f.mu.Unlock()
	if matched == nil {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"unrouted in test"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(matched.status)
	if matched.body != "" {
		_, _ = w.Write([]byte(matched.body))
	}
}

// request returns the single captured request for a method+path.
func (f *fake) request(method, path string) capturedRequest {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var found []capturedRequest
	for _, item := range f.seen {
		if item.method == method && item.path == path {
			found = append(found, item)
		}
	}
	require.Len(f.t, found, 1, "expected exactly one %s %s; saw %v", method, path, f.lines())
	return found[0]
}

func (f *fake) lines() []string {
	out := make([]string, 0, len(f.seen))
	for _, item := range f.seen {
		out = append(out, item.line())
	}
	return out
}

func (f *fake) requireAllRouted() {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Empty(f.t, f.unmatched, "adapter called a path the provider does not publish")
}

// page wraps results in the Management API's paginated envelope.
func page(results string) string {
	return fmt.Sprintf(`{"count":1,"next":null,"previous":null,"results":%s}`, results)
}

// featureRow is one ListFeature as the provider returns it when the request
// carries ?environment=<id>.
func featureRow(id int, name, description string, enabled bool, stateID int) string {
	return fmt.Sprintf(`{"id":%d,"name":%q,"description":%q,"environment_feature_state":{"id":%d,"enabled":%t}}`,
		id, name, description, stateID, enabled)
}

// ---------------------------------------------------------------------------
// 1/7 Enabled — the Flags API identity workflow.
// ---------------------------------------------------------------------------

func TestEnabledIdentifiesTheOrganisationAndReturnsTheProvidersDecision(t *testing.T) {
	service, f := newFake(t, route{http.MethodPost, "/api/v1/identities/", 200,
		`{"identifier":"org_1","traits":[],"flags":[
			{"feature":{"id":11,"name":"beta-dashboard","type":"STANDARD"},"enabled":true,"feature_state_value":null},
			{"feature":{"id":12,"name":"legacy-export","type":"STANDARD"},"enabled":false,"feature_state_value":null}]}`})

	assert.True(t, service.Enabled(context.Background(), "org_1", "beta-dashboard"))

	captured := f.request(http.MethodPost, "/api/v1/identities/")
	assert.Equal(t, "POST /api/v1/identities/", captured.line())
	assert.Equal(t, "ser.env_key_test", captured.environmentKey)
	assert.Equal(t, "application/json", captured.contentType)
	// The public Environment Key is the whole credential on this API; the
	// secret management token must never travel here.
	assert.Empty(t, captured.authorization)

	var body map[string]any
	require.NoError(t, json.Unmarshal(captured.body, &body))
	assert.Equal(t, map[string]any{"identifier": "org_1"}, body)
	f.requireAllRouted()
}

func TestEnabledIsFalseForADisabledFlagAndForAnUnknownKey(t *testing.T) {
	service, _ := newFake(t, route{http.MethodPost, "/api/v1/identities/", 200,
		`{"identifier":"org_1","flags":[{"feature":{"id":12,"name":"legacy-export"},"enabled":false}]}`})
	assert.False(t, service.Enabled(context.Background(), "org_1", "legacy-export"))
	assert.False(t, service.Enabled(context.Background(), "org_1", "never-created"))
}

func TestEnabledWithNoOrganisationReadsTheEnvironmentDefaults(t *testing.T) {
	service, f := newFake(t, route{http.MethodGet, "/api/v1/flags/", 200,
		`[{"feature":{"id":11,"name":"beta-dashboard"},"enabled":true}]`})

	assert.True(t, service.Enabled(context.Background(), "", "beta-dashboard"))

	captured := f.request(http.MethodGet, "/api/v1/flags/")
	assert.Equal(t, "GET /api/v1/flags/", captured.line())
	assert.Equal(t, "ser.env_key_test", captured.environmentKey)
	assert.Empty(t, captured.body)
	f.requireAllRouted()
}

func TestEnabledIsFalseAndLoggedWhenTheProviderFails(t *testing.T) {
	service, _ := newFake(t, route{http.MethodPost, "/api/v1/identities/", 503, `{"detail":"unavailable"}`})
	// The seam gives Enabled no error return: a flag whose state cannot be
	// established must be off, never assumed on.
	assert.False(t, service.Enabled(context.Background(), "org_1", "beta-dashboard"))
}

func TestEnabledCachesOneProviderCallForEveryKeyOfOneOrganisation(t *testing.T) {
	service, f := newFake(t, route{http.MethodPost, "/api/v1/identities/", 200,
		`{"identifier":"org_1","flags":[
			{"feature":{"id":11,"name":"beta-dashboard"},"enabled":true},
			{"feature":{"id":12,"name":"legacy-export"},"enabled":false}]}`})

	assert.True(t, service.Enabled(context.Background(), "org_1", "beta-dashboard"))
	assert.False(t, service.Enabled(context.Background(), "org_1", "legacy-export"))
	assert.True(t, service.Enabled(context.Background(), "org_1", "beta-dashboard"))

	// One decision call answers every key: request() requires exactly one.
	f.request(http.MethodPost, "/api/v1/identities/")
}

// ---------------------------------------------------------------------------
// 2/7 List — the Management API feature list in environment context.
// ---------------------------------------------------------------------------

func TestListReadsTheProjectFeaturesInEnvironmentContext(t *testing.T) {
	service, f := newFake(t, route{http.MethodGet, "/api/v1/projects/7/features/", 200,
		page(`[` + featureRow(11, "beta-dashboard", "New dashboard", true, 501) + `,` +
			featureRow(12, "legacy-export", "", false, 502) + `]`)})

	list, err := service.List(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []flags.Flag{
		{Key: "beta-dashboard", Description: "New dashboard", Enabled: true, Rollout: 100},
		{Key: "legacy-export", Description: "", Enabled: false, Rollout: 100},
	}, list)

	captured := f.request(http.MethodGet, "/api/v1/projects/7/features/")
	assert.Equal(t, "GET /api/v1/projects/7/features/?environment=42", captured.line())
	// The Management API credential is the secret organisation token, sent
	// with the documented `Api-Key` prefix; the public environment key must
	// not be what authorises a mutation-capable API.
	assert.Equal(t, "Api-Key tok_management_test", captured.authorization)
	assert.Empty(t, captured.environmentKey)
	assert.Empty(t, captured.body)
	f.requireAllRouted()
}

func TestListFollowsPaginationAndSurfacesProviderErrors(t *testing.T) {
	service, _ := newFake(t, route{http.MethodGet, "/api/v1/projects/7/features/", 403,
		`{"detail":"You do not have permission to perform this action."}`})
	_, err := service.List(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
	assert.Contains(t, err.Error(), "do not have permission")
	// The failing path is named; no credential appears in the message.
	assert.Contains(t, err.Error(), "/api/v1/projects/7/features/")
	assert.NotContains(t, err.Error(), "tok_management_test")
}

// ---------------------------------------------------------------------------
// 3/7 Upsert — create, then reconcile description and environment state.
// ---------------------------------------------------------------------------

func TestUpsertCreatesAnAbsentFeature(t *testing.T) {
	service, f := newFake(t,
		route{http.MethodGet, "/api/v1/projects/7/features/", 200, page(`[]`)},
		route{http.MethodPost, "/api/v1/projects/7/features/", 201,
			`{"id":13,"name":"beta-dashboard","description":"New dashboard"}`},
	)

	require.NoError(t, service.Upsert(context.Background(), flags.Flag{
		Key: "beta-dashboard", Description: "New dashboard", Enabled: true, Rollout: 100,
	}))

	created := f.request(http.MethodPost, "/api/v1/projects/7/features/")
	assert.Equal(t, "POST /api/v1/projects/7/features/", created.line())
	assert.Equal(t, "Api-Key tok_management_test", created.authorization)
	assert.Equal(t, "application/json", created.contentType)

	var body map[string]any
	require.NoError(t, json.Unmarshal(created.body, &body))
	assert.Equal(t, map[string]any{
		"name": "beta-dashboard", "description": "New dashboard",
		"default_enabled": true, "initial_value": "",
	}, body)

	// The lookup narrows provider-side rather than listing the project.
	lookup := f.request(http.MethodGet, "/api/v1/projects/7/features/")
	assert.Contains(t, lookup.query, "search=beta-dashboard")
	f.requireAllRouted()
}

func TestUpsertUpdatesDescriptionAndEnvironmentStateOfAnExistingFeature(t *testing.T) {
	service, f := newFake(t,
		route{http.MethodGet, "/api/v1/projects/7/features/", 200,
			page(`[` + featureRow(11, "beta-dashboard", "Old copy", false, 501) + `]`)},
		route{http.MethodPut, "/api/v1/projects/7/features/11/", 200, `{"id":11,"description":"New copy"}`},
		route{http.MethodPut, "/api/v1/environments/ser.env_key_test/featurestates/501/", 200,
			`{"id":501,"feature":11,"enabled":true}`},
	)

	require.NoError(t, service.Upsert(context.Background(), flags.Flag{
		Key: "beta-dashboard", Description: "New copy", Enabled: true, Rollout: 100,
	}))

	described := f.request(http.MethodPut, "/api/v1/projects/7/features/11/")
	assert.Equal(t, "PUT /api/v1/projects/7/features/11/", described.line())
	var describedBody map[string]any
	require.NoError(t, json.Unmarshal(described.body, &describedBody))
	// `name`, `default_enabled` and `initial_value` are read-only on update,
	// so the update body carries description alone.
	assert.Equal(t, map[string]any{"description": "New copy"}, describedBody)

	toggled := f.request(http.MethodPut, "/api/v1/environments/ser.env_key_test/featurestates/501/")
	assert.Equal(t, "PUT /api/v1/environments/ser.env_key_test/featurestates/501/", toggled.line())
	assert.Equal(t, "Api-Key tok_management_test", toggled.authorization)
	var toggledBody map[string]any
	require.NoError(t, json.Unmarshal(toggled.body, &toggledBody))
	assert.Equal(t, map[string]any{"feature": float64(11), "enabled": true}, toggledBody)
	f.requireAllRouted()
}

func TestUpsertLeavesAnUnchangedFeatureAlone(t *testing.T) {
	service, f := newFake(t, route{http.MethodGet, "/api/v1/projects/7/features/", 200,
		page(`[` + featureRow(11, "beta-dashboard", "Same copy", true, 501) + `]`)})

	require.NoError(t, service.Upsert(context.Background(), flags.Flag{
		Key: "beta-dashboard", Description: "Same copy", Enabled: true, Rollout: 100,
	}))
	// No PUT was issued: requireAllRouted proves nothing unrouted was tried,
	// and only the lookup route exists.
	assert.Equal(t, []string{"GET /api/v1/projects/7/features/?environment=42&search=beta-dashboard"}, f.lines())
	f.requireAllRouted()
}

func TestUpsertRefusesAPartialRolloutByNameRatherThanDroppingIt(t *testing.T) {
	service, f := newFake(t)
	err := service.Upsert(context.Background(), flags.Flag{Key: "beta-dashboard", Enabled: true, Rollout: 50})
	require.ErrorIs(t, err, ErrRolloutUnsupported)
	assert.Contains(t, err.Error(), "percentage-split segment override")
	assert.Contains(t, err.Error(), "rollout 50")
	// Refused before any provider call, so a rejected rollout cannot leave a
	// half-written feature behind.
	assert.Empty(t, f.lines())
}

// ---------------------------------------------------------------------------
// 4/7 Delete.
// ---------------------------------------------------------------------------

func TestDeleteRemovesTheFeatureFromTheProject(t *testing.T) {
	service, f := newFake(t,
		route{http.MethodGet, "/api/v1/projects/7/features/", 200,
			page(`[` + featureRow(11, "beta-dashboard", "", true, 501) + `]`)},
		route{http.MethodDelete, "/api/v1/projects/7/features/11/", 204, ""},
	)

	require.NoError(t, service.Delete(context.Background(), "beta-dashboard"))

	deleted := f.request(http.MethodDelete, "/api/v1/projects/7/features/11/")
	assert.Equal(t, "DELETE /api/v1/projects/7/features/11/", deleted.line())
	assert.Equal(t, "Api-Key tok_management_test", deleted.authorization)
	assert.Empty(t, deleted.body)
	f.requireAllRouted()
}

func TestDeleteIsIdempotentForAnAbsentKey(t *testing.T) {
	service, f := newFake(t, route{http.MethodGet, "/api/v1/projects/7/features/", 200, page(`[]`)})
	require.NoError(t, service.Delete(context.Background(), "never-created"))
	f.requireAllRouted()
}

// ---------------------------------------------------------------------------
// 5/7 ListOverrides — identity overrides, selected by the provider.
// ---------------------------------------------------------------------------

func TestListOverridesAsksTheProviderForIdentityOverridesOnly(t *testing.T) {
	service, f := newFake(t, route{http.MethodGet, "/api/v1/environments/ser.env_key_test/featurestates/", 200,
		page(`[
			{"id":901,"enabled":true,"feature":11,"identity":{"id":31,"identifier":"org_1"}},
			{"id":902,"enabled":false,"feature":11,"identity":{"id":32,"identifier":"org_2"}},
			{"id":903,"enabled":true,"feature":11,"identity":null}]`)})

	overrides, err := service.ListOverrides(context.Background(), "beta-dashboard")
	require.NoError(t, err)
	assert.Equal(t, []flags.Override{
		{OrgID: "org_1", Enabled: true},
		{OrgID: "org_2", Enabled: false},
	}, overrides)

	captured := f.request(http.MethodGet, "/api/v1/environments/ser.env_key_test/featurestates/")
	assert.Equal(t,
		"GET /api/v1/environments/ser.env_key_test/featurestates/?anyIdentity=1&feature_name=beta-dashboard",
		captured.line())
	assert.Equal(t, "Api-Key tok_management_test", captured.authorization)
	f.requireAllRouted()
}

func TestListOverridesReturnsEmptyRatherThanNilForAFlagWithNone(t *testing.T) {
	service, _ := newFake(t, route{http.MethodGet, "/api/v1/environments/ser.env_key_test/featurestates/", 200, page(`[]`)})
	overrides, err := service.ListOverrides(context.Background(), "beta-dashboard")
	require.NoError(t, err)
	assert.Empty(t, overrides)
}

// ---------------------------------------------------------------------------
// 6/7 SetOverride.
// ---------------------------------------------------------------------------

func TestSetOverrideCreatesAnIdentityOverrideForAKnownOrganisation(t *testing.T) {
	service, f := newFake(t,
		route{http.MethodGet, "/api/v1/projects/7/features/", 200,
			page(`[` + featureRow(11, "beta-dashboard", "", false, 501) + `]`)},
		route{http.MethodGet, "/api/v1/environments/ser.env_key_test/identities/", 200,
			page(`[{"id":31,"identifier":"org_1","environment":42}]`)},
		route{http.MethodGet, "/api/v1/environments/ser.env_key_test/identities/31/featurestates/", 200, page(`[]`)},
		route{http.MethodPost, "/api/v1/environments/ser.env_key_test/identities/31/featurestates/", 201,
			`{"id":901,"feature":11,"enabled":true}`},
	)

	require.NoError(t, service.SetOverride(context.Background(), "beta-dashboard", "org_1", true))

	identity := f.request(http.MethodGet, "/api/v1/environments/ser.env_key_test/identities/")
	// A quoted query is the provider's exact-identifier match, so "org_1"
	// cannot be satisfied by "org_10".
	assert.Equal(t, `GET /api/v1/environments/ser.env_key_test/identities/?q=%22org_1%22`, identity.line())

	created := f.request(http.MethodPost, "/api/v1/environments/ser.env_key_test/identities/31/featurestates/")
	assert.Equal(t, "POST /api/v1/environments/ser.env_key_test/identities/31/featurestates/", created.line())
	assert.Equal(t, "Api-Key tok_management_test", created.authorization)
	var body map[string]any
	require.NoError(t, json.Unmarshal(created.body, &body))
	assert.Equal(t, map[string]any{"feature": float64(11), "enabled": true}, body)
	f.requireAllRouted()
}

func TestSetOverrideUpdatesAnExistingIdentityOverride(t *testing.T) {
	service, f := newFake(t,
		route{http.MethodGet, "/api/v1/projects/7/features/", 200,
			page(`[` + featureRow(11, "beta-dashboard", "", false, 501) + `]`)},
		route{http.MethodGet, "/api/v1/environments/ser.env_key_test/identities/", 200,
			page(`[{"id":31,"identifier":"org_1"}]`)},
		route{http.MethodGet, "/api/v1/environments/ser.env_key_test/identities/31/featurestates/", 200,
			page(`[{"id":901,"enabled":true,"feature":11}]`)},
		route{http.MethodPut, "/api/v1/environments/ser.env_key_test/identities/31/featurestates/901/", 200,
			`{"id":901,"feature":11,"enabled":false}`},
	)

	require.NoError(t, service.SetOverride(context.Background(), "beta-dashboard", "org_1", false))

	updated := f.request(http.MethodPut, "/api/v1/environments/ser.env_key_test/identities/31/featurestates/901/")
	assert.Equal(t, "PUT /api/v1/environments/ser.env_key_test/identities/31/featurestates/901/", updated.line())
	var body map[string]any
	require.NoError(t, json.Unmarshal(updated.body, &body))
	assert.Equal(t, map[string]any{"feature": float64(11), "enabled": false}, body)
	f.requireAllRouted()
}

func TestSetOverrideCreatesTheIdentityWhenTheOrganisationHasNeverBeenSeen(t *testing.T) {
	service, f := newFake(t,
		route{http.MethodGet, "/api/v1/projects/7/features/", 200,
			page(`[` + featureRow(11, "beta-dashboard", "", false, 501) + `]`)},
		route{http.MethodGet, "/api/v1/environments/ser.env_key_test/identities/", 200, page(`[]`)},
		route{http.MethodPost, "/api/v1/environments/ser.env_key_test/identities/", 201,
			`{"id":77,"identifier":"org_new","environment":42}`},
		route{http.MethodGet, "/api/v1/environments/ser.env_key_test/identities/77/featurestates/", 200, page(`[]`)},
		route{http.MethodPost, "/api/v1/environments/ser.env_key_test/identities/77/featurestates/", 201,
			`{"id":905,"feature":11,"enabled":true}`},
	)

	require.NoError(t, service.SetOverride(context.Background(), "beta-dashboard", "org_new", true))

	created := f.request(http.MethodPost, "/api/v1/environments/ser.env_key_test/identities/")
	var body map[string]any
	require.NoError(t, json.Unmarshal(created.body, &body))
	assert.Equal(t, map[string]any{"identifier": "org_new"}, body)
	f.requireAllRouted()
}

func TestSetOverrideRefusesAnUnknownFlagAndAnEmptyOrganisation(t *testing.T) {
	service, _ := newFake(t, route{http.MethodGet, "/api/v1/projects/7/features/", 200, page(`[]`)})
	err := service.SetOverride(context.Background(), "never-created", "org_1", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `no feature named "never-created"`)

	service2, _ := newFake(t, route{http.MethodGet, "/api/v1/projects/7/features/", 200,
		page(`[` + featureRow(11, "beta-dashboard", "", false, 501) + `]`)})
	err = service2.SetOverride(context.Background(), "beta-dashboard", "", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "organisation id names the identity")
}

// ---------------------------------------------------------------------------
// 7/7 DeleteOverride.
// ---------------------------------------------------------------------------

func TestDeleteOverrideRemovesTheIdentityOverride(t *testing.T) {
	service, f := newFake(t,
		route{http.MethodGet, "/api/v1/projects/7/features/", 200,
			page(`[` + featureRow(11, "beta-dashboard", "", false, 501) + `]`)},
		route{http.MethodGet, "/api/v1/environments/ser.env_key_test/identities/", 200,
			page(`[{"id":31,"identifier":"org_1"}]`)},
		route{http.MethodGet, "/api/v1/environments/ser.env_key_test/identities/31/featurestates/", 200,
			page(`[{"id":901,"enabled":true,"feature":11}]`)},
		route{http.MethodDelete, "/api/v1/environments/ser.env_key_test/identities/31/featurestates/901/", 204, ""},
	)

	require.NoError(t, service.DeleteOverride(context.Background(), "beta-dashboard", "org_1"))

	deleted := f.request(http.MethodDelete, "/api/v1/environments/ser.env_key_test/identities/31/featurestates/901/")
	assert.Equal(t, "DELETE /api/v1/environments/ser.env_key_test/identities/31/featurestates/901/", deleted.line())
	assert.Equal(t, "Api-Key tok_management_test", deleted.authorization)
	assert.Empty(t, deleted.body)
	f.requireAllRouted()
}

func TestDeleteOverrideIsIdempotentWhenThereIsNothingToRemove(t *testing.T) {
	// Unknown flag: no identity lookup is even attempted.
	service, f := newFake(t, route{http.MethodGet, "/api/v1/projects/7/features/", 200, page(`[]`)})
	require.NoError(t, service.DeleteOverride(context.Background(), "never-created", "org_1"))
	f.requireAllRouted()

	// Known flag, unknown organisation.
	service2, f2 := newFake(t,
		route{http.MethodGet, "/api/v1/projects/7/features/", 200,
			page(`[` + featureRow(11, "beta-dashboard", "", false, 501) + `]`)},
		route{http.MethodGet, "/api/v1/environments/ser.env_key_test/identities/", 200, page(`[]`)},
	)
	require.NoError(t, service2.DeleteOverride(context.Background(), "beta-dashboard", "org_1"))
	f2.requireAllRouted()

	// Known flag, known organisation, no override on that flag.
	service3, f3 := newFake(t,
		route{http.MethodGet, "/api/v1/projects/7/features/", 200,
			page(`[` + featureRow(11, "beta-dashboard", "", false, 501) + `]`)},
		route{http.MethodGet, "/api/v1/environments/ser.env_key_test/identities/", 200,
			page(`[{"id":31,"identifier":"org_1"}]`)},
		route{http.MethodGet, "/api/v1/environments/ser.env_key_test/identities/31/featurestates/", 200, page(`[]`)},
	)
	require.NoError(t, service3.DeleteOverride(context.Background(), "beta-dashboard", "org_1"))
	f3.requireAllRouted()
}

// ---------------------------------------------------------------------------
// Seam conformance, boot refusal, health.
// ---------------------------------------------------------------------------

func TestServiceSatisfiesTheWholeSeamWithoutErrReadOnly(t *testing.T) {
	var service any = New(Service{})
	_, isService := service.(flags.Service)
	require.True(t, isService, "adapter must satisfy every flags.Service method")

	// The reason this vendor replaced LaunchDarkly: not one method is a
	// read-only stub.
	failing := New(Service{EnvironmentKey: "k", Token: "t", ProjectID: 1, EnvironmentID: 1,
		Management: "http://127.0.0.1:1", Flags: "http://127.0.0.1:1", Cache: newTestCache()})
	ctx := context.Background()
	for name, err := range map[string]error{
		"Upsert":         failing.Upsert(ctx, flags.Flag{Key: "k", Rollout: 100}),
		"Delete":         failing.Delete(ctx, "k"),
		"SetOverride":    failing.SetOverride(ctx, "k", "org_1", true),
		"DeleteOverride": failing.DeleteOverride(ctx, "k", "org_1"),
	} {
		require.Error(t, err, "%s must reach the provider", name)
		assert.NotErrorIs(t, err, flags.ErrReadOnly, "%s must not be a read-only stub", name)
	}
	_, err := failing.ListOverrides(ctx, "k")
	require.Error(t, err)
	assert.NotErrorIs(t, err, flags.ErrReadOnly)
}

func TestNewModuleRefusesEveryMissingCredentialInOneError(t *testing.T) {
	host := apphost.Map(map[string]string{}, time.Unix(0, 0).UTC(), "test")
	_, err := NewModule(context.Background(), host, Deps{Cache: newTestCache()})
	require.Error(t, err)
	message := err.Error()
	for _, key := range []string{
		"FLAGSMITH_ENVIRONMENT_KEY",
		"FLAGSMITH_MANAGEMENT_TOKEN",
		"FLAGSMITH_PROJECT_ID",
		"FLAGSMITH_ENVIRONMENT_ID",
	} {
		assert.Contains(t, message, key, "one boot refusal must name every missing key")
	}
	// The refusal is a refusal: no local fallback service is handed back.
	assert.True(t, strings.HasPrefix(message, "flagsmith: "))
}

func TestNewModuleRefusesAMalformedNumericCoordinate(t *testing.T) {
	host := apphost.Map(map[string]string{
		"FLAGSMITH_ENVIRONMENT_KEY":  "ser.k",
		"FLAGSMITH_MANAGEMENT_TOKEN": "tok",
		"FLAGSMITH_PROJECT_ID":       "seven",
		"FLAGSMITH_ENVIRONMENT_ID":   "0",
	}, time.Unix(0, 0).UTC(), "test")
	_, err := NewModule(context.Background(), host, Deps{Cache: newTestCache()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `FLAGSMITH_PROJECT_ID must be a positive integer, got "seven"`)
	assert.Contains(t, err.Error(), `FLAGSMITH_ENVIRONMENT_ID must be a positive integer, got "0"`)
}

func TestNewModuleBuildsTheAdapterFromItsOwnDeclaredEnvironment(t *testing.T) {
	host := apphost.Map(map[string]string{
		"FLAGSMITH_ENVIRONMENT_KEY":  "ser.env_key",
		"FLAGSMITH_MANAGEMENT_TOKEN": "tok_secret",
		"FLAGSMITH_PROJECT_ID":       "7",
		"FLAGSMITH_ENVIRONMENT_ID":   "42",
		"FLAGSMITH_CONSOLE":          "https://app.flagsmith.com/project/7",
	}, time.Unix(0, 0).UTC(), "test")
	module, err := NewModule(context.Background(), host, Deps{Cache: newTestCache()})
	require.NoError(t, err)
	require.NotNil(t, module)
	assert.Equal(t, defaultFlagsBaseURL, module.Value.Flags)
	assert.Equal(t, defaultManagementBaseURL, module.Value.Management)
	assert.Equal(t, 7, module.Value.ProjectID)
	assert.Equal(t, 42, module.Value.EnvironmentID)
	assert.Equal(t, "https://app.flagsmith.com/project/7", module.Value.Console)
	require.NoError(t, module.Health(context.Background()))
}

func TestNewModuleRefusesAMissingCacheCapability(t *testing.T) {
	host := apphost.Map(map[string]string{}, time.Unix(0, 0).UTC(), "test")
	_, err := NewModule(context.Background(), host, Deps{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cache store is required")
}

func TestEndpointOverridesSelectASelfHostedDeployment(t *testing.T) {
	host := apphost.Map(map[string]string{
		"FLAGSMITH_ENVIRONMENT_KEY":  "ser.env_key",
		"FLAGSMITH_MANAGEMENT_TOKEN": "tok_secret",
		"FLAGSMITH_PROJECT_ID":       "1",
		"FLAGSMITH_ENVIRONMENT_ID":   "1",
		"FLAGSMITH_API_URL":          "https://flags.internal.example/",
		"FLAGSMITH_FLAGS_API_URL":    "https://flags.internal.example/",
	}, time.Unix(0, 0).UTC(), "test")
	module, err := NewModule(context.Background(), host, Deps{Cache: newTestCache()})
	require.NoError(t, err)
	assert.Equal(t, "https://flags.internal.example", module.Value.Management)
	assert.Equal(t, "https://flags.internal.example", module.Value.Flags)
}

func TestHealthMatchesTheSeamSiblingsAndMakesNoProviderCall(t *testing.T) {
	unreachable := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("Health must not spend a provider round trip")
	}))
	t.Cleanup(unreachable.Close)

	require.Error(t, (&Service{}).Health(context.Background()), "unconfigured adapter must be unhealthy")
	require.NoError(t, New(Service{
		Flags: unreachable.URL, Management: unreachable.URL,
		EnvironmentKey: "k", Token: "t", ProjectID: 1, EnvironmentID: 1, Cache: newTestCache(),
	}).Health(context.Background()))

	// A managed adapter with no cache would cost a provider round trip on
	// every flag check of every request; that state is unhealthy, not quiet.
	require.Error(t, New(Service{
		EnvironmentKey: "k", Token: "t", ProjectID: 1, EnvironmentID: 1,
	}).Health(context.Background()))

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, New(Service{
		EnvironmentKey: "k", Token: "t", ProjectID: 1, EnvironmentID: 1, Cache: newTestCache(),
	}).Health(cancelled), context.Canceled)
}

func TestModuleErrorsNameTheAdapterNotTheCredential(t *testing.T) {
	service := New(Service{Token: "super-secret-token", EnvironmentKey: "ser.public",
		ProjectID: 7, EnvironmentID: 42, Cache: newTestCache(),
		Management: "http://127.0.0.1:1", Flags: "http://127.0.0.1:1"})
	err := service.Delete(context.Background(), "beta-dashboard")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "super-secret-token")
}

func TestReadyNamesEveryMissingCoordinateForAHandBuiltService(t *testing.T) {
	err := (&Service{}).ready()
	require.Error(t, err)
	for _, key := range []string{
		"FLAGSMITH_MANAGEMENT_TOKEN", "FLAGSMITH_ENVIRONMENT_KEY",
		"FLAGSMITH_PROJECT_ID", "FLAGSMITH_ENVIRONMENT_ID",
	} {
		assert.Contains(t, err.Error(), key)
	}
	// One joined error, not the first failure: every coordinate is a
	// separate wrapped cause an operator can enumerate.
	joined, ok := err.(interface{ Unwrap() []error })
	require.True(t, ok, "ready must return errors.Join, got %T", err)
	assert.Len(t, joined.Unwrap(), 4)
}

// ---------------------------------------------------------------------------
// The cache double this adapter's own tests use.
//
// Not internal/cache/memory. That package is ggg/system/cache-memory — an
// ADAPTER — and ValidatePayloadAdapterImports refuses one adapter's payload
// importing another's, because an adapter is a per-environment provider
// selection that no `requires` can express: this file would compile only while
// providers["ggg/cache"] happened to select cache-memory. The import went
// unnoticed for a release because nothing installed this module at all; the
// verification install that finally does is what surfaced it.
//
// It lives in this file, owned by ggg/system/feature-flags-flagsmith, which
// the rule exempts. A double in the ggg/system/cache seam was the alternative
// and is the wrong one here: internal/cache/memory is ALREADY the in-memory
// cache, so a second one in the seam is duplication two modules would have to
// keep in sync — unlike search.NewMemory and realtime.NewMemory, whose
// adapters are Postgres and have nothing in-memory to duplicate.
//
// It honours the TTL rather than ignoring it, because decisionTTL is the
// bound this adapter's staleness argument rests on and a double that drops it
// would make the caching test pass for the wrong reason.
type testCache struct {
	mu    sync.Mutex
	items map[string]testCacheItem
}

type testCacheItem struct {
	value   []byte
	expires time.Time
}

func newTestCache() *testCache { return &testCache{items: map[string]testCacheItem{}} }

func (c *testCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.items[key]
	if !ok {
		return nil, false, nil
	}
	if !item.expires.IsZero() && time.Now().After(item.expires) {
		delete(c.items, key)
		return nil, false, nil
	}
	return append([]byte(nil), item.value...), true, nil
}

func (c *testCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	expires := time.Time{}
	if ttl > 0 {
		expires = time.Now().Add(ttl)
	}
	c.mu.Lock()
	c.items[key] = testCacheItem{append([]byte(nil), value...), expires}
	c.mu.Unlock()
	return nil
}

func (c *testCache) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	delete(c.items, key)
	c.mu.Unlock()
	return nil
}

var _ cache.Store = (*testCache)(nil)
