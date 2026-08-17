package billing

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPolarFake spins an httptest server that records the last request and
// replies with the given status/body. base is rewritten to the fake so no
// real traffic happens.
func newPolarFake(t *testing.T, status int, body string, capture *capturedRequest) *PolarClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		capture.path = r.URL.Path
		capture.method = r.Method
		capture.auth = r.Header.Get("Authorization")
		capture.version = r.Header.Get("Polar-Version")
		capture.contentType = r.Header.Get("Content-Type")
		capture.body = b
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c := NewPolarClient("tok_test", "sandbox")
	c.baseURL = srv.URL
	return c
}

type capturedRequest struct {
	path, method, auth, version, contentType string
	body                                     []byte
}

func TestPolarCreateCheckout(t *testing.T) {
	var cap capturedRequest
	c := newPolarFake(t, 200, `{"url":"https://checkout.polar.sh/c/abc"}`, &cap)
	url, err := c.CreateCheckout(context.Background(), CheckoutParams{
		ProductID: "prod_123", SuccessURL: "https://app.test/billing/return",
		CustomerExternalID: "org_1", Metadata: map[string]string{"org_id": "org_1"},
	})
	require.NoError(t, err)
	assert.Equal(t, "https://checkout.polar.sh/c/abc", url)

	assert.Equal(t, "/v1/checkouts/", cap.path)
	assert.Equal(t, http.MethodPost, cap.method)
	assert.Equal(t, "Bearer tok_test", cap.auth)
	assert.Equal(t, "2026-04", cap.version)
	assert.Equal(t, "application/json", cap.contentType)

	var body map[string]any
	require.NoError(t, json.Unmarshal(cap.body, &body))
	assert.Equal(t, []any{"prod_123"}, body["products"])
	assert.Equal(t, "https://app.test/billing/return", body["success_url"])
	assert.Equal(t, "org_1", body["external_customer_id"])
	assert.Equal(t, map[string]any{"org_id": "org_1"}, body["metadata"])
}

func TestPolarCreatePortalSession(t *testing.T) {
	var cap capturedRequest
	c := newPolarFake(t, 200, `{"customer_portal_url":"https://portal.polar.sh/x"}`, &cap)
	url, err := c.CreatePortalSession(context.Background(), "org_1")
	require.NoError(t, err)
	assert.Equal(t, "https://portal.polar.sh/x", url)
	assert.Equal(t, "/v1/customer-sessions/", cap.path)
	assert.Equal(t, http.MethodPost, cap.method)

	var body map[string]any
	require.NoError(t, json.Unmarshal(cap.body, &body))
	assert.Equal(t, "org_1", body["external_customer_id"])
}

func TestPolarRevokeSubscription(t *testing.T) {
	var cap capturedRequest
	c := newPolarFake(t, 200, `{}`, &cap)
	require.NoError(t, c.RevokeSubscription(context.Background(), "sub_42"))
	assert.Equal(t, "/v1/subscriptions/sub_42", cap.path)
	assert.Equal(t, http.MethodDelete, cap.method)
}

func TestPolarRevokeSubscriptionAlreadyRevoked(t *testing.T) {
	var cap capturedRequest
	c := newPolarFake(t, 403, `{"error":"already revoked"}`, &cap)
	err := c.RevokeSubscription(context.Background(), "sub_42")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
	assert.Contains(t, err.Error(), "already revoked")
}

func TestPolarIngestUsage(t *testing.T) {
	var cap capturedRequest
	c := newPolarFake(t, 200, `{"inserted":2,"duplicates":0}`, &cap)
	err := c.IngestUsage(context.Background(), "org_1", []UsageEvent{
		{Name: "ai_tokens", ExternalID: "ue-1", Value: 150, Metadata: map[string]any{"model": "gpt"}},
		{Name: "ai_tokens", Value: 1},
	})
	require.NoError(t, err)
	assert.Equal(t, "/v1/events/ingest", cap.path)
	assert.Equal(t, http.MethodPost, cap.method)

	var body struct {
		Events []map[string]any `json:"events"`
	}
	require.NoError(t, json.Unmarshal(cap.body, &body))
	require.Len(t, body.Events, 2)

	first := body.Events[0]
	assert.Equal(t, "ai_tokens", first["name"])
	assert.Equal(t, "org_1", first["external_customer_id"])
	assert.Equal(t, "ue-1", first["external_id"])
	md, ok := first["metadata"].(map[string]any)
	require.True(t, ok, "metadata is an object")
	assert.Equal(t, "gpt", md["model"])
	assert.Equal(t, float64(150), md["value"], "non-1 value folded into metadata")

	second := body.Events[1]
	md2, ok := second["metadata"].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, md2, "value", "value == 1 is omitted")
	assert.NotContains(t, second, "external_id", "empty external id omitted")
}

func TestPolarIngestUsageEmpty(t *testing.T) {
	var cap capturedRequest
	c := newPolarFake(t, 200, `{}`, &cap)
	require.NoError(t, c.IngestUsage(context.Background(), "org_1", nil))
	assert.Empty(t, cap.path, "no HTTP call for empty batch")
}

func TestPolarServerErrorIncludesBody(t *testing.T) {
	var cap capturedRequest
	c := newPolarFake(t, 500, `{"detail":"boom `+string(make([]byte, 600))+`"}`, &cap)
	_, err := c.CreatePortalSession(context.Background(), "org_1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
	assert.LessOrEqual(t, len(err.Error()), 500+len("polar: POST /v1/customer-sessions/: 500: "), "body truncated")
}

func TestNewPolarClientServerFallback(t *testing.T) {
	assert.Equal(t, "https://api.polar.sh", NewPolarClient("t", "production").baseURL)
	assert.Equal(t, "https://sandbox-api.polar.sh", NewPolarClient("t", "sandbox").baseURL)
	assert.Equal(t, "https://sandbox-api.polar.sh", NewPolarClient("t", "nonsense").baseURL, "unknown server falls back to sandbox")
}
