package polar

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"

	"github.com/gogogadget/gogogadget/internal/billing"
	billingcontract "github.com/gogogadget/gogogadget/internal/billing/contract"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClientContract runs the shared billing contract against the real HTTP
// client pointed at an httptest fake: a healthy fake for success paths, a
// 500-ing fake for provider-error paths. The seam's mock and the local
// adapter run the identical table.
func TestClientContract(t *testing.T) {
	billingcontract.RunClient(t,
		func(t *testing.T) billing.Client {
			var cap capturedRequest
			// One body satisfying both URL-returning methods.
			return newPolarFake(t, http.StatusOK,
				`{"url":"https://checkout.polar.test/c/x","customer_portal_url":"https://portal.polar.test/x"}`, &cap)
		},
		func(t *testing.T, _ string) billing.Client {
			var cap capturedRequest
			return newPolarFake(t, http.StatusInternalServerError, `{"detail":"boom"}`, &cap)
		})
}

// contractWebhookSecret is the raw secret format Polar hands out through its
// dashboard and local CLI.
const contractWebhookSecret = "0123456789abcdef0123456789abcdef"

// TestWebhookContract proves a Standard-Webhooks-signed Polar delivery
// round-trips the neutral subscription event and that a delivery signed with
// another secret is refused.
func TestWebhookContract(t *testing.T) {
	billingcontract.RunWebhook(t, Provider, func(t *testing.T) billingcontract.WebhookHarness {
		t.Helper()
		return billingcontract.WebhookHarness{
			Webhook: Webhook{Secret: contractWebhookSecret},
			Deliver: func(t *testing.T, want billing.SubscriptionEvent) ([]byte, http.Header) {
				t.Helper()
				payload := polarPayload(t, want)
				return payload, signStandard(t, contractWebhookSecret, want.ID, payload)
			},
			Tamper: func(t *testing.T) ([]byte, http.Header) {
				t.Helper()
				payload := polarPayload(t, billingcontract.SubscriptionEventFixture())
				return payload, signStandard(t, "ffffffffffffffffffffffffffffffff", "msg_tampered", payload)
			},
		}
	})
}

// TestWebhookRefusesUnsignedDelivery pins that the hosted adapter never
// accepts a delivery carrying only the message id.
func TestWebhookRefusesUnsignedDelivery(t *testing.T) {
	payload := polarPayload(t, billingcontract.SubscriptionEventFixture())
	_, err := Webhook{Secret: contractWebhookSecret}.Verify(context.Background(), payload,
		http.Header{"Webhook-Id": []string{"msg_unsigned"}})
	require.Error(t, err)
}

// TestWebhookResolvesOrgFromCustomerExternalID pins the payload detail the
// receiver never sees: with no checkout metadata, the customer's external id
// is the org id.
func TestWebhookResolvesOrgFromCustomerExternalID(t *testing.T) {
	payload := []byte(`{"type":"subscription.created","data":{"id":"sub_x","customer_id":"cust_x",
		"product_id":"prod_x","status":"active","customer":{"external_id":"org_from_customer"}}}`)
	evt, err := Webhook{Secret: contractWebhookSecret}.Verify(context.Background(), payload,
		signStandard(t, contractWebhookSecret, "msg_ext", payload))
	require.NoError(t, err)
	assert.Equal(t, "org_from_customer", evt.OrgIDHint)
	assert.Equal(t, Provider, evt.Provider)
}

// TestWebhookCarriesPeriodBounds pins the fields the neutral fixture leaves
// optional: the hosted adapter does carry the renewal date and the
// cancel-at-period-end flag, and the dunning state machine depends on them.
func TestWebhookCarriesPeriodBounds(t *testing.T) {
	end := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second)
	want := billingcontract.SubscriptionEventFixture()
	want.CurrentPeriodEnd, want.CancelAtPeriodEnd = end, true
	payload := polarPayload(t, want)
	evt, err := Webhook{Secret: contractWebhookSecret}.Verify(context.Background(), payload,
		signStandard(t, contractWebhookSecret, want.ID, payload))
	require.NoError(t, err)
	assert.Equal(t, end, evt.CurrentPeriodEnd.UTC())
	assert.True(t, evt.CancelAtPeriodEnd)
}

func TestModuleRefusesMissingCredentials(t *testing.T) {
	_, err := NewModule(context.Background(), nil, Deps{})
	require.Error(t, err, "config is required")
	_, err = NewModule(context.Background(), nil, Deps{Config: &config.Config{}})
	require.Error(t, err, "POLAR_ACCESS_TOKEN is required")
	_, err = NewModule(context.Background(), nil, Deps{Config: &config.Config{PolarAccessToken: "polar_test"}})
	require.Error(t, err, "POLAR_WEBHOOK_SECRET is required")
}

// polarPayload encodes a neutral subscription event in Polar's wire format.
func polarPayload(t *testing.T, evt billing.SubscriptionEvent) []byte {
	t.Helper()
	out, err := json.Marshal(map[string]any{
		"type": evt.Type,
		"data": map[string]any{
			"id":                   evt.ProviderSubscriptionID,
			"customer_id":          evt.ProviderCustomerID,
			"product_id":           evt.ProviderProductID,
			"status":               evt.Status,
			"current_period_end":   evt.CurrentPeriodEnd,
			"cancel_at_period_end": evt.CancelAtPeriodEnd,
			"metadata":             map[string]string{"org_id": evt.OrgIDHint},
		},
	})
	require.NoError(t, err)
	return out
}

// signStandard emits webhook-* headers using the raw secret format Polar
// supplies.
func signStandard(t *testing.T, secret, msgID string, payload []byte) http.Header {
	t.Helper()
	wh, err := standardwebhooks.NewWebhookRaw([]byte(secret))
	require.NoError(t, err)
	now := time.Now()
	sig, err := wh.Sign(msgID, now, payload)
	require.NoError(t, err)
	h := http.Header{}
	h.Set("webhook-id", msgID)
	h.Set("webhook-timestamp", strconv.FormatInt(now.Unix(), 10))
	h.Set("webhook-signature", sig)
	return h
}
