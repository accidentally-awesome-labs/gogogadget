package billing

import "context"

// Client is the billing seam: handlers never import the Polar SDK directly.
// Swapping providers means replacing one file (PolarClient).
type Client interface {
	CreateCheckout(ctx context.Context, p CheckoutParams) (checkoutURL string, err error)
	CreatePortalSession(ctx context.Context, customerExternalID string) (portalURL string, err error)
	RevokeSubscription(ctx context.Context, polarSubscriptionID string) error
}

type CheckoutParams struct {
	ProductID, SuccessURL, CustomerExternalID string
	Metadata                                  map[string]string
}
