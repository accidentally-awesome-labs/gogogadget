package billinglocal

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gogogadget/gogogadget/internal/billing"
	billingcontract "github.com/gogogadget/gogogadget/internal/billing/contract"
	"github.com/stretchr/testify/require"
)

// TestClientContract runs the shared billing contract against the local
// zero-account client. The hosted adapter and the seam's mock run the
// identical table.
func TestClientContract(t *testing.T) {
	billingcontract.RunClient(t,
		func(t *testing.T) billing.Client { return New("http://localhost:18080") },
		nil)
}

// TestWebhookContract proves the local envelope round-trips the neutral
// subscription event. It carries no signature: local checkout confirmations
// are posted by the authenticated in-app screen, and the hosted receiver
// refuses provider "local" outright.
func TestWebhookContract(t *testing.T) {
	billingcontract.RunWebhook(t, "local", func(t *testing.T) billingcontract.WebhookHarness {
		t.Helper()
		return billingcontract.WebhookHarness{
			Webhook: LocalWebhook{},
			Deliver: func(t *testing.T, want billing.SubscriptionEvent) ([]byte, http.Header) {
				t.Helper()
				out, err := json.Marshal(map[string]any{
					"type": want.Type,
					"data": map[string]any{
						"id":          want.ProviderSubscriptionID,
						"status":      want.Status,
						"product_id":  want.ProviderProductID,
						"customer_id": want.ProviderCustomerID,
						"org_id":      want.OrgIDHint,
					},
				})
				require.NoError(t, err)
				h := http.Header{}
				h.Set("id", want.ID)
				return out, h
			},
		}
	})
}
