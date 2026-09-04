package polar

import (
	"context"
	"encoding/json"
	"net/http"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"

	"github.com/gogogadget/gogogadget/internal/billing"
)

// messageIDHeader is Polar's delivery id. Polar signs with the Standard
// Webhooks scheme, so its header family is webhook-id/webhook-timestamp/
// webhook-signature — a provider detail confined to this package. Clerk's
// svix-* family is why one verification library cannot cover both providers.
const messageIDHeader = "webhook-id"

// Webhook is the Polar webhook receiver contract: Standard Webhooks signature
// verification plus Polar payload parsing, out through
// billing.SubscriptionEvent.
type Webhook struct{ Secret string }

func (w Webhook) Verify(_ context.Context, payload []byte, headers http.Header) (billing.SubscriptionEvent, error) {
	wh, err := standardwebhooks.NewWebhookRaw([]byte(w.Secret))
	if err != nil {
		return billing.SubscriptionEvent{}, err
	}
	if err = wh.Verify(payload, headers); err != nil {
		return billing.SubscriptionEvent{}, err
	}
	var envelope struct {
		Type string                      `json:"type"`
		Data billing.SubscriptionPayload `json:"data"`
	}
	if err = json.Unmarshal(payload, &envelope); err != nil {
		return billing.SubscriptionEvent{}, err
	}
	return billing.SubscriptionEvent{
		ID:                     headers.Get(messageIDHeader),
		Provider:               Provider,
		Type:                   envelope.Type,
		OrgIDHint:              envelope.Data.OrgID(),
		ProviderSubscriptionID: envelope.Data.ID,
		ProviderCustomerID:     envelope.Data.CustomerID,
		ProviderProductID:      envelope.Data.ProductID,
		Status:                 envelope.Data.Status,
		CurrentPeriodEnd:       envelope.Data.CurrentPeriodEnd,
		CancelAtPeriodEnd:      envelope.Data.CancelAtPeriodEnd,
	}, nil
}

var _ billing.BillingWebhook = Webhook{}
