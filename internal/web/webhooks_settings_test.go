package web

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/webhooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func webhookEndpointParams(orgID string) sqlc.InsertWebhookEndpointParams {
	return sqlc.InsertWebhookEndpointParams{
		OrgID: orgID, CreatedBy: "user_seed", Url: "https://hooks.example.com/x",
		Secret: webhooks.NewSecret(), EventTypes: []string{}, Description: "",
	}
}

func itoa64(v int64) string { return strconv.FormatInt(v, 10) }

// enableWebhooks turns the webhooks flag on for tests that drive the UI.
func enableWebhooks(t *testing.T, s *Server) {
	t.Helper()
	require.NoError(t, s.q.UpsertFeatureFlag(t.Context(), sqlc.UpsertFeatureFlagParams{
		Key: "webhooks", Description: "", Enabled: true, Rollout: 100,
	}))
	t.Cleanup(func() {
		_, _ = s.db.Exec(context.Background(), "DELETE FROM feature_flags WHERE key = 'webhooks'")
	})
}

func TestWebhookEndpointCRUDAndSecretShownOnce(t *testing.T) {
	s := integrationServer(t, nil)
	enableWebhooks(t, s)
	seedMembership(t, s, "user_whep", "org_whep", "org:admin")
	cookie := sessionCookie("user_whep", "org_whep", "org:admin")

	// Page renders create form + empty deliveries.
	code, _, body := serve(t, s, "GET", "/app/settings/webhooks", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `data-testid="webhook-url"`)
	assert.Contains(t, body, "project.created")

	// Create: secret revealed ONCE in the response…
	token, csrfCookies := csrfFor(t, s)
	form := url.Values{"url": {"https://hooks.example.com/ggg"}, "description": {"staging"}, "event_types": {"project.created"}}
	h := http.Header{}
	h.Set("X-CSRF-Token", token)
	h.Set("Content-Type", "application/x-www-form-urlencoded")
	h.Set("HX-Request", "true")
	code, _, body = serve(t, s, "POST", "/app/settings/webhooks/endpoints", []byte(form.Encode()), h, append(csrfCookies, cookie)...)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `data-testid="webhook-secret-reveal"`)
	m := regexp.MustCompile(`whsec_[A-Za-z0-9+/=_-]{30,}`).FindString(body)
	require.NotEmpty(t, m, "secret present in create response")

	// …and NOT on the plain list page afterwards.
	code, _, body = serve(t, s, "GET", "/app/settings/webhooks", nil, nil, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "webhook-secret-reveal")
	assert.NotContains(t, body, m, "secret never re-rendered")
	assert.Contains(t, body, "https://hooks.example.com/ggg")
	assert.Contains(t, body, `data-testid="webhook-endpoint-row"`)

	// http (not https) URL → 422.
	form = url.Values{"url": {"http://hooks.example.com/ggg"}}
	code, _, body = serve(t, s, "POST", "/app/settings/webhooks/endpoints", []byte(form.Encode()), h, append(csrfCookies, cookie)...)
	require.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Contains(t, body, "https://")

	// No event checkboxes → nil form value must not 500 (all-events endpoint).
	form = url.Values{"url": {"https://hooks.example.com/all"}}
	code, _, body = serve(t, s, "POST", "/app/settings/webhooks/endpoints", []byte(form.Encode()), h, append(csrfCookies, cookie)...)
	require.Equal(t, http.StatusOK, code, "no checkboxes = all events")
	assert.Contains(t, body, "https://hooks.example.com/all")
}

func TestWebhookEndpointToggleAndCrossOrg404(t *testing.T) {
	s := integrationServer(t, nil)
	enableWebhooks(t, s)
	seedMembership(t, s, "user_wht", "org_wht", "org:admin")
	seedMembership(t, s, "user_whx", "org_whx", "org:admin")

	ep, err := s.q.InsertWebhookEndpoint(t.Context(), webhookEndpointParams("org_wht"))
	require.NoError(t, err)

	// Cross-org toggle → 404.
	token, csrfCookies := csrfFor(t, s)
	h := http.Header{}
	h.Set("X-CSRF-Token", token)
	h.Set("Content-Type", "application/x-www-form-urlencoded")
	h.Set("HX-Request", "true")
	code, _, _ := serve(t, s, "POST", "/app/settings/webhooks/endpoints/"+itoa64(ep.ID)+"/disable", nil, h,
		append(csrfCookies, sessionCookie("user_whx", "org_whx", "org:admin"))...)
	assert.Equal(t, http.StatusNotFound, code)

	// Owner disables → badge flips.
	code, _, body := serve(t, s, "POST", "/app/settings/webhooks/endpoints/"+itoa64(ep.ID)+"/disable", nil, h,
		append(csrfCookies, sessionCookie("user_wht", "org_wht", "org:admin"))...)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "disabled")

	// Re-enable → enabled badge back.
	code, _, body = serve(t, s, "POST", "/app/settings/webhooks/endpoints/"+itoa64(ep.ID)+"/enable", nil, h,
		append(csrfCookies, sessionCookie("user_wht", "org_wht", "org:admin"))...)
	require.Equal(t, http.StatusOK, code)
	assert.True(t, strings.Contains(body, `data-testid="webhook-endpoint-row"`))
}

func TestWebhookDeliveryReplayResets(t *testing.T) {
	s := integrationServer(t, nil)
	enableWebhooks(t, s)
	seedMembership(t, s, "user_whr", "org_whr", "org:admin")
	ctx := t.Context()

	ep, err := s.q.InsertWebhookEndpoint(ctx, webhookEndpointParams("org_whr"))
	require.NoError(t, err)
	d, err := s.q.InsertWebhookDelivery(ctx, sqlc.InsertWebhookDeliveryParams{
		EndpointID: ep.ID, OrgID: "org_whr", EventType: "project.created", Payload: []byte(`{"type":"project.created","data":{"id":1}}`),
	})
	require.NoError(t, err)
	require.NoError(t, s.q.MarkDeliveryDead(ctx, sqlc.MarkDeliveryDeadParams{ID: d.ID, LastError: "test"}))
	t.Cleanup(func() {
		_, _ = s.db.Exec(ctx, "DELETE FROM webhook_deliveries WHERE org_id = 'org_whr'")
		_, _ = s.db.Exec(ctx, "DELETE FROM webhook_endpoints WHERE org_id = 'org_whr'")
		_, _ = s.db.Exec(ctx, "DELETE FROM jobs WHERE kind = 'webhook.deliver'")
	})

	cookie := sessionCookie("user_whr", "org_whr", "org:admin")
	token, csrfCookies := csrfFor(t, s)
	h := http.Header{}
	h.Set("X-CSRF-Token", token)
	h.Set("Content-Type", "application/x-www-form-urlencoded")
	h.Set("HX-Request", "true")
	code, _, _ := serve(t, s, "POST", "/app/settings/webhooks/deliveries/"+itoa64(d.ID)+"/replay", nil, h, append(csrfCookies, cookie)...)
	require.Equal(t, http.StatusOK, code)

	row, err := s.q.GetWebhookDelivery(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", row.Status)
	assert.Equal(t, int32(0), row.Attempts)
	assert.False(t, row.DeliveredAt.Valid)

	// A deliver job was enqueued.
	var n int
	require.NoError(t, s.db.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE kind = 'webhook.deliver'").Scan(&n))
	assert.Equal(t, 1, n)
}

func TestEmitCreatesDeliveriesAndJobs(t *testing.T) {
	s := integrationServer(t, nil)
	enableWebhooks(t, s)
	seedMembership(t, s, "user_emit", "org_emit", "org:admin")
	ctx := t.Context()

	// Endpoint subscribed to everything.
	_, err := s.q.InsertWebhookEndpoint(ctx, webhookEndpointParams("org_emit"))
	require.NoError(t, err)
	// Endpoint subscribed to project.deleted only — must NOT get created events.
	_, err = s.q.InsertWebhookEndpoint(ctx, sqlc.InsertWebhookEndpointParams{
		OrgID: "org_emit", CreatedBy: "user_seed", Url: "https://hooks.example.com/filtered",
		Secret: webhooks.NewSecret(), EventTypes: []string{"project.deleted"}, Description: "",
	})
	require.NoError(t, err)
	// Disabled endpoint — never gets anything.
	disabledEp, err := s.q.InsertWebhookEndpoint(ctx, webhookEndpointParams("org_emit"))
	require.NoError(t, err)
	require.NoError(t, s.q.SetWebhookEndpointDisabled(ctx, sqlc.SetWebhookEndpointDisabledParams{ID: disabledEp.ID, Disabled: true, OrgID: "org_emit"}))
	t.Cleanup(func() {
		_, _ = s.db.Exec(ctx, "DELETE FROM webhook_deliveries WHERE org_id = 'org_emit'")
		_, _ = s.db.Exec(ctx, "DELETE FROM webhook_endpoints WHERE org_id = 'org_emit'")
		_, _ = s.db.Exec(ctx, "DELETE FROM jobs WHERE kind = 'webhook.deliver'")
	})

	webhooks.Emit(ctx, s.q, "org_emit", "project.created", map[string]any{"id": 1, "name": "X", "status": "active", "org_id": "org_emit"})

	var deliveries int
	require.NoError(t, s.db.QueryRow(ctx, "SELECT count(*) FROM webhook_deliveries WHERE org_id = 'org_emit'").Scan(&deliveries))
	assert.Equal(t, 1, deliveries, "only the all-events endpoint matched")

	var jobsCount int
	require.NoError(t, s.db.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE kind = 'webhook.deliver'").Scan(&jobsCount))
	assert.Equal(t, 1, jobsCount)

	// Envelope shape.
	var payload []byte
	require.NoError(t, s.db.QueryRow(ctx, "SELECT payload FROM webhook_deliveries WHERE org_id = 'org_emit'").Scan(&payload))
	assert.Contains(t, string(payload), `"type": "project.created"`)
	assert.Contains(t, string(payload), `"occurred_at"`)
}

func TestWebhookSecretRotation(t *testing.T) {
	s := integrationServer(t, nil)
	enableWebhooks(t, s)
	seedMembership(t, s, "user_whr", "org_whr", "org:admin")
	seedMembership(t, s, "user_whz", "org_whz", "org:admin")

	ep, err := s.q.InsertWebhookEndpoint(t.Context(), webhookEndpointParams("org_whr"))
	require.NoError(t, err)
	original := ep.Secret

	token, csrfCookies := csrfFor(t, s)
	h := http.Header{}
	h.Set("X-CSRF-Token", token)
	h.Set("Content-Type", "application/x-www-form-urlencoded")
	h.Set("HX-Request", "true")

	// Cross-org rotation → 404, secret untouched.
	code, _, _ := serve(t, s, "POST", "/app/settings/webhooks/endpoints/"+itoa64(ep.ID)+"/rotate", nil, h,
		append(csrfCookies, sessionCookie("user_whz", "org_whz", "org:admin"))...)
	require.Equal(t, http.StatusNotFound, code)
	unchanged, err := s.q.GetWebhookEndpoint(t.Context(), sqlc.GetWebhookEndpointParams{ID: ep.ID, OrgID: "org_whr"})
	require.NoError(t, err)
	assert.Equal(t, original, unchanged.Secret)

	// Owner rotates → new secret revealed ONCE, old one kept for the grace window.
	code, _, body := serve(t, s, "POST", "/app/settings/webhooks/endpoints/"+itoa64(ep.ID)+"/rotate", nil, h,
		append(csrfCookies, sessionCookie("user_whr", "org_whr", "org:admin"))...)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `data-testid="webhook-secret-reveal"`)
	assert.Contains(t, body, `data-testid="webhook-rotate-note"`, "grace window explained on rotation")

	rotated, err := s.q.GetWebhookEndpoint(t.Context(), sqlc.GetWebhookEndpointParams{ID: ep.ID, OrgID: "org_whr"})
	require.NoError(t, err)
	assert.NotEqual(t, original, rotated.Secret, "new secret minted")
	assert.Equal(t, original, rotated.SecretPrevious, "old secret retained for the grace window")
	assert.True(t, rotated.SecretRotatedAt.Valid)
	assert.Contains(t, body, rotated.Secret, "plaintext shown exactly once, in this response")

	// Re-render without rotating: the secret is never shown again.
	code, _, body = serve(t, s, "GET", "/app/settings/webhooks", nil, nil,
		sessionCookie("user_whr", "org_whr", "org:admin"))
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, rotated.Secret)
	assert.NotContains(t, body, `data-testid="webhook-secret-reveal"`)

	// Rotation is audited.
	rows, err := s.q.ListAuditAll(t.Context(), sqlc.ListAuditAllParams{Filter: "webhook_endpoint.secret_rotated", Off: 0, Lim: 10})
	require.NoError(t, err)
	assert.Len(t, rows, 1)
}
