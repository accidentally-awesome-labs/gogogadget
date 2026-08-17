package billing

import "context"

// Client is the billing seam: handlers never import the Polar SDK directly.
// Swapping providers means replacing one file (PolarClient).
type Client interface {
	CreateCheckout(ctx context.Context, p CheckoutParams) (checkoutURL string, err error)
	CreatePortalSession(ctx context.Context, customerExternalID string) (portalURL string, err error)
	RevokeSubscription(ctx context.Context, polarSubscriptionID string) error
	IngestUsage(ctx context.Context, customerExternalID string, events []UsageEvent) error
}

type CheckoutParams struct {
	ProductID, SuccessURL, CustomerExternalID string
	Metadata                                  map[string]string
}

// UsageEvent is one metered-usage datum sent to Polar's events API. Polar
// deduplicates on ExternalID when set.
type UsageEvent struct {
	Name       string
	ExternalID string
	Value      int64
	Metadata   map[string]any
}
