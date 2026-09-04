// Package contract is the provider-neutral billing contract. It lives beside
// the seam and is imported by every adapter's own test package, so the local
// adapter, the hosted adapter, and the in-memory double are held to one
// identical table instead of a provider-shaped table inside the seam.
package contract

import (
	"context"
	"net/http"
	"testing"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RunClient is the Client contract: every implementation must pass it, so a
// provider swap can't drift from the behavior handlers rely on.
//
// factory returns a healthy client. errFactory returns a client whose next
// call to the named method fails with a provider error, or nil when the
// implementation cannot inject a failure for that method — the error case is
// then skipped loudly instead of silently dropped.
func RunClient(t *testing.T, factory func(t *testing.T) billing.Client, errFactory func(t *testing.T, method string) billing.Client) {
	t.Helper()

	methods := []struct {
		name string
		call func(t *testing.T, ctx context.Context, c billing.Client) error
	}{
		{"CreateCheckout", func(t *testing.T, ctx context.Context, c billing.Client) error {
			url, err := c.CreateCheckout(ctx, billing.CheckoutParams{
				ProductID:          "prod_contract",
				SuccessURL:         "https://app.test/billing/return",
				CustomerExternalID: "org_contract",
				Metadata:           map[string]string{"org_id": "org_contract"},
			})
			if err == nil {
				assert.NotEmpty(t, url, "success must return a checkout URL")
			}
			return err
		}},
		{"CreatePortalSession", func(t *testing.T, ctx context.Context, c billing.Client) error {
			url, err := c.CreatePortalSession(ctx, "org_contract")
			if err == nil {
				assert.NotEmpty(t, url, "success must return a portal URL")
			}
			return err
		}},
		{"RevokeSubscription", func(t *testing.T, ctx context.Context, c billing.Client) error {
			return c.RevokeSubscription(ctx, "sub_contract")
		}},
		{"IngestUsage", func(t *testing.T, ctx context.Context, c billing.Client) error {
			return c.IngestUsage(ctx, "org_contract", []billing.UsageEvent{
				{Name: "ai_tokens", ExternalID: "ue-contract", Value: 150, Metadata: map[string]any{"model": "gpt"}},
			})
		}},
	}

	for _, m := range methods {
		t.Run(m.name+"/success", func(t *testing.T) {
			require.NoError(t, m.call(t, context.Background(), factory(t)))
		})
		t.Run(m.name+"/provider_error", func(t *testing.T) {
			if errFactory == nil {
				t.Skip("implementation cannot inject provider errors")
			}
			c := errFactory(t, m.name)
			if c == nil {
				t.Skipf("implementation cannot inject a provider error for %s", m.name)
			}
			require.Error(t, m.call(t, context.Background(), c))
		})
	}
}

// WebhookHarness adapts one adapter's wire format to the neutral webhook
// contract. Deliver encodes the requested neutral event as this adapter's own
// signed delivery.
type WebhookHarness struct {
	Webhook billing.BillingWebhook
	Deliver func(t *testing.T, want billing.SubscriptionEvent) (payload []byte, headers http.Header)
	// Tamper returns a delivery the adapter must refuse. It is nil for
	// adapters that perform no signature verification.
	Tamper func(t *testing.T) (payload []byte, headers http.Header)
}

// SubscriptionEventFixture is the canonical event every billing adapter must
// round-trip. It carries only the fields the neutral vocabulary requires of
// every adapter: period bounds are optional, so an adapter whose envelope has
// no notion of them (the local zero-account one) still conforms, and the
// adapters that do carry them pin that in their own suites.
func SubscriptionEventFixture() billing.SubscriptionEvent {
	return billing.SubscriptionEvent{
		ID:                     "msg_contract",
		Type:                   "subscription.created",
		OrgIDHint:              "org_contract",
		ProviderSubscriptionID: "sub_contract",
		ProviderCustomerID:     "cust_contract",
		ProviderProductID:      "prod_contract",
		Status:                 "active",
	}
}

// RunWebhook is the BillingWebhook contract: an adapter must round-trip the
// canonical subscription event, stamp its own provider name, and refuse a
// delivery it cannot authenticate.
func RunWebhook(t *testing.T, provider string, harness func(t *testing.T) WebhookHarness) {
	t.Helper()
	h := harness(t)
	require.NotNil(t, h.Webhook, "harness must supply a webhook")
	require.NotNil(t, h.Deliver, "harness must supply a delivery encoder")
	ctx := context.Background()

	t.Run("subscription event round-trips", func(t *testing.T) {
		want := SubscriptionEventFixture()
		payload, headers := h.Deliver(t, want)
		got, err := h.Webhook.Verify(ctx, payload, headers)
		require.NoError(t, err)
		want.Provider = provider
		assert.Equal(t, want, got)
	})

	t.Run("malformed payload errors", func(t *testing.T) {
		_, headers := h.Deliver(t, SubscriptionEventFixture())
		_, err := h.Webhook.Verify(ctx, []byte("{"), headers)
		assert.Error(t, err)
	})

	if h.Tamper != nil {
		t.Run("unauthenticated delivery errors", func(t *testing.T) {
			payload, headers := h.Tamper(t)
			_, err := h.Webhook.Verify(ctx, payload, headers)
			assert.Error(t, err)
		})
	}
}
