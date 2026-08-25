package jobs

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
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
		{"http://example.com/hook", true},          // non-https
		{"https://localhost/hook", true},           // loopback
		{"https://127.0.0.1/hook", true},           // loopback IP
		{"https://10.0.0.8/hook", true},            // private
		{"https://192.168.1.1/hook", true},         // private
		{"https://169.254.169.254/latest", true},   // link-local (cloud metadata)
		{"https://0.0.0.0/hook", true},             // unspecified
		{"https://nonexistent.invalid/hook", true}, // unresolvable
		{"https://example.com/hook", false},        // public, resolvable
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
	require.NoError(t, w.deliverWebhook(ctx, WebhookDeliverPayload{DeliveryID: d.ID}, Attempt{Number: 1, Max: 8}))

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
	err := w.deliverWebhook(ctx, WebhookDeliverPayload{DeliveryID: d.ID}, Attempt{Number: 1, Max: 8})
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
	// The last attempt: ClaimJob has already incremented, so attempt 8 of 8 is
	// the one ProcessOne dead-letters on.
	err := w.deliverWebhook(ctx, WebhookDeliverPayload{DeliveryID: d.ID}, Attempt{Number: 8, Max: 8})
	require.Error(t, err)

	row, err := q.GetWebhookDelivery(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, "dead", row.Status)

	// The endpoint owner got an in-app notification.
	n, err := q.CountUnreadByUser(ctx, sqlc.CountUnreadByUserParams{ClerkOrgID: "org_wh", ClerkUserID: "user_wh"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
}

// The customer-visible delivery status and the queue must agree on when a
// delivery is permanently dead. On the second-to-last attempt the queue still
// has a retry left, so the endpoint owner must not yet be told it failed
// permanently — and must not get the notification that goes with it.
func TestWebhookDeliverStaysPendingWhileQueueWillRetry(t *testing.T) {
	w, q, pool, epID := webhookTestSetup(t)
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	pointEndpoint(t, pool, epID, srv.URL)

	d := insertDelivery(t, q, epID)
	require.Error(t, w.deliverWebhook(ctx, WebhookDeliverPayload{DeliveryID: d.ID}, Attempt{Number: 7, Max: 8}))

	row, err := q.GetWebhookDelivery(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", row.Status, "attempt 7 of 8 leaves a retry, so the delivery is not dead yet")

	n, err := q.CountUnreadByUser(ctx, sqlc.CountUnreadByUserParams{ClerkOrgID: "org_wh", ClerkUserID: "user_wh"})
	require.NoError(t, err)
	assert.Equal(t, int64(0), n, "no permanent-failure notification while retries remain")
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

func TestSigningSecretsGraceWindow(t *testing.T) {
	now := time.Now()
	ep := sqlc.WebhookEndpoint{Secret: "whsec_new"}

	// No rotation yet → current secret only.
	assert.Equal(t, []string{"whsec_new"}, signingSecrets(ep, now))

	// Inside the window → both, current first.
	ep.SecretPrevious = "whsec_old"
	ep.SecretRotatedAt = pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true}
	assert.Equal(t, []string{"whsec_new", "whsec_old"}, signingSecrets(ep, now))

	// Window closed → current only (the janitor clears the column later).
	ep.SecretRotatedAt = pgtype.Timestamptz{Time: now.Add(-WebhookRotationGrace - time.Minute), Valid: true}
	assert.Equal(t, []string{"whsec_new"}, signingSecrets(ep, now))

	// Defensive: a previous secret without a timestamp never signs.
	ep.SecretRotatedAt = pgtype.Timestamptz{}
	assert.Equal(t, []string{"whsec_new"}, signingSecrets(ep, now))
}

func TestWebhookDeliverSignsWithBothSecretsDuringGrace(t *testing.T) {
	w, q, pool, epID := webhookTestSetup(t)
	ctx := context.Background()

	oldSecret := "whsec_" + "b2xkLXNlY3JldC10aGF0LWlzLWxvbmctZW5vdWdoLTAxMjM="
	newSecret := "whsec_" + "bmV3LXNlY3JldC10aGF0LWlzLWxvbmctZW5vdWdoLTAxMjM="
	_, err := pool.Exec(ctx, `UPDATE webhook_endpoints SET secret = $1, secret_previous = $2, secret_rotated_at = now() WHERE id = $3`,
		newSecret, oldSecret, epID)
	require.NoError(t, err)

	var gotHeader http.Header
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		rw.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	pointEndpoint(t, pool, epID, srv.URL)

	d := insertDelivery(t, q, epID)
	require.NoError(t, w.deliverWebhook(ctx, WebhookDeliverPayload{DeliveryID: d.ID}, Attempt{Number: 1, Max: 8}))

	// Two space-delimited signatures — a receiver holding EITHER secret verifies.
	sigs := strings.Fields(gotHeader.Get("webhook-signature"))
	require.Len(t, sigs, 2, "grace window signs with both secrets")
	for _, secret := range []string{newSecret, oldSecret} {
		wh, err := standardwebhooks.NewWebhookRaw([]byte(secret))
		require.NoError(t, err)
		require.NoError(t, wh.Verify(gotBody, gotHeader), "receiver with %s must verify", secret[:12])
	}

	// Past the window: only the current secret signs, and the old one fails.
	_, err = pool.Exec(ctx, `UPDATE webhook_endpoints SET secret_rotated_at = now() - interval '48 hours' WHERE id = $1`, epID)
	require.NoError(t, err)
	d2 := insertDelivery(t, q, epID)
	require.NoError(t, w.deliverWebhook(ctx, WebhookDeliverPayload{DeliveryID: d2.ID}, Attempt{Number: 1, Max: 8}))
	require.Len(t, strings.Fields(gotHeader.Get("webhook-signature")), 1)
	oldWh, err := standardwebhooks.NewWebhookRaw([]byte(oldSecret))
	require.NoError(t, err)
	require.Error(t, oldWh.Verify(gotBody, gotHeader), "rotated-out secret stops verifying after the window")
}

func TestJanitorClearsExpiredPreviousSecrets(t *testing.T) {
	w, q, pool, epID := webhookTestSetup(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `UPDATE webhook_endpoints SET secret_previous = 'whsec_old', secret_rotated_at = now() - interval '48 hours' WHERE id = $1`, epID)
	require.NoError(t, err)

	w.janitorPass(ctx)

	ep, err := q.GetWebhookEndpoint(ctx, sqlc.GetWebhookEndpointParams{ID: epID, ClerkOrgID: "org_wh"})
	require.NoError(t, err)
	assert.Empty(t, ep.SecretPrevious, "expired previous secret cleared")
	assert.False(t, ep.SecretRotatedAt.Valid)
}
