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
//
// All four provider-error cases are the declared omission set, and the reason
// is that this ADAPTER HAS NO FAILURE MODE, not that the methods are somehow
// unfailable in principle: CreateCheckout builds a query string,
// CreatePortalSession concatenates one, RevokeSubscription deletes a map key
// and IngestUsage is `return nil`. The only error the package returns is the
// nil-receiver guard. Giving it an injectable failure would mean adding a
// failure mode to a shipped production adapter so that a test table could
// exercise it.
func TestClientContract(t *testing.T) {
	billingcontract.RunClient(t,
		func(t *testing.T) billing.Client { return New("http://localhost:18080") },
		nil,
		"CreateCheckout", "CreatePortalSession", "RevokeSubscription", "IngestUsage")
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
