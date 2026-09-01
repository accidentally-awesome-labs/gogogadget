package billinglocal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/config"
)

type Deps struct{ Config *config.Config }
type Module struct {
	Client  *Client
	Catalog billing.PlanCatalog
	Webhook billing.BillingWebhook
}

func NewModule(ctx context.Context, _ apphost.Host, d Deps) (*Module, error) {
	if d.Config == nil {
		return nil, fmt.Errorf("billing local: config dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Module{Client: New(d.Config.AppURL), Catalog: LocalPlanCatalog(), Webhook: LocalWebhook{}}, nil
}

type LocalWebhook struct{}

func (LocalWebhook) Verify(_ context.Context, payload []byte, headers http.Header) (billing.SubscriptionEvent, error) {
	var raw struct {
		Type string `json:"type"`
		Data struct {
			ID         string `json:"id"`
			Status     string `json:"status"`
			ProductID  string `json:"product_id"`
			CustomerID string `json:"customer_id"`
			OrgID      string `json:"org_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return billing.SubscriptionEvent{}, err
	}
	if raw.Type == "" || raw.Data.CustomerID == "" {
		return billing.SubscriptionEvent{}, fmt.Errorf("billing-local: invalid event")
	}
	return billing.SubscriptionEvent{ID: headers.Get("id"), Provider: "local", Type: raw.Type, OrgIDHint: raw.Data.OrgID, ProviderSubscriptionID: raw.Data.ID, ProviderCustomerID: raw.Data.CustomerID, ProviderProductID: raw.Data.ProductID, Status: raw.Data.Status}, nil
}

var _ billing.Client = (*Client)(nil)
var _ billing.BillingWebhook = LocalWebhook{}
