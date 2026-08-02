package billing

import (
	"context"
	"errors"

	polargo "github.com/polarsource/polar-go"
	"github.com/polarsource/polar-go/models/components"
	"github.com/polarsource/polar-go/models/operations"
)

// PolarClient implements Client with the official Polar SDK. Every
// Speakeasy-generated, pointer-heavy call shape stays confined to this file.
type PolarClient struct {
	sdk *polargo.Polar
}

func NewPolarClient(accessToken, server string) *PolarClient {
	if server != "sandbox" && server != "production" {
		server = "sandbox"
	}
	return &PolarClient{sdk: polargo.New(polargo.WithSecurity(accessToken), polargo.WithServer(server))}
}

func (c *PolarClient) CreateCheckout(ctx context.Context, p CheckoutParams) (string, error) {
	metadata := map[string]components.CheckoutCreateMetadata{}
	for k, v := range p.Metadata {
		metadata[k] = components.CreateCheckoutCreateMetadataStr(v)
	}
	res, err := c.sdk.Checkouts.Create(ctx, components.CheckoutCreate{
		Products:           []string{p.ProductID},
		SuccessURL:         &p.SuccessURL,
		ExternalCustomerID: &p.CustomerExternalID,
		Metadata:           metadata,
	})
	if err != nil {
		return "", err
	}
	if res.Checkout == nil || res.Checkout.URL == "" {
		return "", errors.New("polar: checkout returned no URL")
	}
	return res.Checkout.URL, nil
}

func (c *PolarClient) CreatePortalSession(ctx context.Context, customerExternalID string) (string, error) {
	res, err := c.sdk.CustomerSessions.Create(ctx,
		operations.CreateCustomerSessionsCreateCustomerSessionCreateCustomerSessionCustomerExternalIDCreate(
			components.CustomerSessionCustomerExternalIDCreate{ExternalCustomerID: customerExternalID},
		),
	)
	if err != nil {
		return "", err
	}
	if res.CustomerSession == nil || res.CustomerSession.CustomerPortalURL == "" {
		return "", errors.New("polar: portal session returned no URL")
	}
	return res.CustomerSession.CustomerPortalURL, nil
}

func (c *PolarClient) RevokeSubscription(ctx context.Context, polarSubscriptionID string) error {
	_, err := c.sdk.Subscriptions.Revoke(ctx, polarSubscriptionID)
	return err
}
