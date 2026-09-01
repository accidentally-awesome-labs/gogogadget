// Package billing defines the constructor-free provider-neutral billing seam.
package billing

import (
	"context"
	"errors"
)

// Deps are supplied by a selected billing adapter. The seam does not inspect
// credentials and never chooses a provider.
type Deps struct {
	Client  Client
	Catalog PlanCatalog
	Webhook BillingWebhook
}

type Module struct {
	Client  Client
	Catalog PlanCatalog
	Webhook BillingWebhook
}

func NewModule(_ context.Context, d Deps) (*Module, error) {
	if d.Client == nil || d.Catalog == nil || d.Webhook == nil {
		return nil, errors.New("billing: all capabilities are required")
	}
	return &Module{Client: d.Client, Catalog: d.Catalog, Webhook: d.Webhook}, nil
}
