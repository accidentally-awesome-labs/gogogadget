package jobs

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgxpool"
	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebhookSigningRoundTrip(t *testing.T) {
	secret := "whsec_" + "dGVzdC1zZWNyZXQtdGhhdC1pcy1sb25nLWVub3VnaA=="
	wh, err := standardwebhooks.NewWebhookRaw([]byte(secret))
	require.NoError(t, err)
	payload := []byte(`{"type":"project.created","occurred_at":"2026-08-16T00:00:00Z","data":{"id":1}}`)

	msgID := "msg_test_1"
	ts := time.Now()
	sig, err := wh.Sign(msgID, ts, payload)
	require.NoError(t, err)

	// Verify like an inbound customer endpoint would.
	// http.Header.Get canonicalizes keys — build with Set, not a literal.
	hdr := http.Header{}
	hdr.Set("webhook-id", msgID)
	hdr.Set("webhook-timestamp", strconv.FormatInt(ts.Unix(), 10))
	hdr.Set("webhook-signature", sig)
	require.NoError(t, wh.Verify(payload, hdr))

	// Tampered payload must NOT verify.
	require.Error(t, wh.Verify([]byte(`{"type":"project.deleted"}`), hdr))
}

func TestWebhookSSRFGuard(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		rawURL  string
		wantErr bool
	}{
		{"http://example.com/hook", true},       // non-https
		{"https://localhost/hook", true},        // loopback
		{"https://127.0.0.1/hook", true},        // loopback IP
		{"https://10.0.0.8/hook", true},         // private
		{"https://192.168.1.1/hook", true},      // private
		{"https://169.254.169.254/latest", true},// link-local (cloud metadata)
		{"https://0.0.0.0/hook", true},           // unspecified
		{"https://nonexistent.invalid/hook", true}, // unresolvable
		{"https://example.com/hook", false},     // public, resolvable
	}
	for _, c := range cases {
		err := guardWebhookURL(ctx, c.rawURL)
		if c.wantErr {
			assert.Error(t, err, c.rawURL)
		} else {
			assert.NoError(t, err, c.rawURL)
		}
	}
}

// webhookTestSetup seeds an org+user+endpoint and returns the worker with the
// guard relaxed for the httptest fake receiver.
func webhookTestSetup(t *testing.T) (*Worker, *sqlc.Queries, *pgxpool.Pool, int64) {
	t.Helper()
	pool, q := testSetup(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, "INSERT INTO users (clerk_user_id, email, name, avatar_url) VALUES ('user_wh', 'wh@example.com', 'WH', '') ON CONFLICT DO NOTHING")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "INSERT INTO orgs (clerk_org_id, name, slug) VALUES ('org_wh', 'WH Org', 'wh') ON CONFLICT DO NOTHING")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "INSERT INTO org_members (clerk_org_id, clerk_user_id, role) VALUES ('org_wh', 'user_wh', 'org:admin') ON CONFLICT DO NOTHING")
	require.NoError(t, err)
	w := testWorker(q, t.TempDir())
	w.WebhookGuard = func(context.Context, string) error { return nil }
	w.WebhookTransport = http.DefaultTransport.(*http.Transport).Clone()
	ep, err := q.InsertWebhookEndpoint(ctx, sqlc.InsertWebhookEndpointParams{
		ClerkOrgID: "org_wh", CreatedBy: "user_wh", Url: "https://placeholder.invalid", Secret: "whsec_test", EventTypes: []string{}, Description: "",
	})
	require.NoError(t, err)
	return w, q, pool, ep.ID
}

// pointEndpoint repoints the seeded endpoint at the httptest fake receiver
// (direct SQL: no production query edits endpoint URLs — the settings UI
// recreates endpoints instead).
func pointEndpoint(t *testing.T, pool *pgxpool.Pool, epID int64, rawURL string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), "UPDATE webhook_endpoints SET url = $1 WHERE id = $2", rawURL, epID)
	require.NoError(t, err)
}

func insertDelivery(t *testing.T, q *sqlc.Queries, endpointID int64) sqlc.WebhookDelivery {
	t.Helper()
	d, err := q.InsertWebhookDelivery(context.Background(), sqlc.InsertWebhookDeliveryParams{
		EndpointID: endpointID, ClerkOrgID: "org_wh", EventType: "project.created",
		Payload: []byte(`{"type":"project.created","data":{"id":7}}`),
	})
	require.NoError(t, err)
	return d
}

func TestWebhookDeliverSuccess(t *testing.T) {
	w, q, pool, epID := webhookTestSetup(t)
	ctx := context.Background()

	var gotSig, gotTS, gotID string
	var gotPayload []byte
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("webhook-signature")
		gotTS = r.Header.Get("webhook-timestamp")
		gotID = r.Header.Get("webhook-id")
		gotPayload, _ = io.ReadAll(r.Body)
		rw.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	// Point the endpoint at the fake receiver.
	pointEndpoint(t, pool, epID, srv.URL)

	d := insertDelivery(t, q, epID)
	require.NoError(t, w.deliverWebhook(ctx, sqlc.Job{ID: 1, Payload: mustJSON(t, WebhookDeliverPayload{DeliveryID: d.ID}), MaxAttempts: 8}))

	row, err := q.GetWebhookDelivery(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, "success", row.Status)
	assert.True(t, row.DeliveredAt.Valid)
	assert.Equal(t, int32(200), row.LastResponseStatus.Int32)

	// Signature verifies against the endpoint secret.
	wh, err := standardwebhooks.NewWebhookRaw([]byte("whsec_test"))
	require.NoError(t, err)
	gotHdr := http.Header{}
	gotHdr.Set("webhook-id", gotID)
	gotHdr.Set("webhook-timestamp", gotTS)
	gotHdr.Set("webhook-signature", gotSig)
	require.NoError(t, wh.Verify(gotPayload, gotHdr), "customer-side verification must pass")
}

func TestWebhookDeliver5xxRetries(t *testing.T) {
	w, q, pool, epID := webhookTestSetup(t)
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	pointEndpoint(t, pool, epID, srv.URL)

	d := insertDelivery(t, q, epID)
	err := w.deliverWebhook(ctx, sqlc.Job{ID: 2, Payload: mustJSON(t, WebhookDeliverPayload{DeliveryID: d.ID}), MaxAttempts: 8})
	require.Error(t, err, "5xx returns error → backoff path")

	row, err := q.GetWebhookDelivery(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", row.Status, "still pending while retries remain")
	assert.Equal(t, int32(500), row.LastResponseStatus.Int32)
	assert.Equal(t, int32(1), row.Attempts)
}

func TestWebhookDeliverDeadLetterNotifies(t *testing.T) {
	w, q, pool, epID := webhookTestSetup(t)
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	pointEndpoint(t, pool, epID, srv.URL)

	d := insertDelivery(t, q, epID)
	// Final attempt (attempts already at max-1 → this failure exhausts).
	err := w.deliverWebhook(ctx, sqlc.Job{ID: 3, Payload: mustJSON(t, WebhookDeliverPayload{DeliveryID: d.ID}), Attempts: 7, MaxAttempts: 8})
	require.Error(t, err)

	row, err := q.GetWebhookDelivery(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, "dead", row.Status)

	// The endpoint owner got an in-app notification.
	n, err := q.CountUnreadByUser(ctx, sqlc.CountUnreadByUserParams{ClerkOrgID: "org_wh", ClerkUserID: "user_wh"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

