package billing

import "context"

// MockClient is the billing test double — no HTTP mocking of the SDK.
type MockClient struct {
	CheckoutURL string
	PortalURL   string
	RevokeErr   error

	CheckoutCalls []CheckoutParams
	RevokedIDs    []string
}

func (m *MockClient) CreateCheckout(_ context.Context, p CheckoutParams) (string, error) {
	m.CheckoutCalls = append(m.CheckoutCalls, p)
	if m.CheckoutURL == "" {
		return "https://checkout.example.test/session", nil
	}
	return m.CheckoutURL, nil
}

func (m *MockClient) CreatePortalSession(_ context.Context, customerExternalID string) (string, error) {
	if m.PortalURL == "" {
		return "https://portal.example.test/" + customerExternalID, nil
	}
	return m.PortalURL, nil
}

func (m *MockClient) RevokeSubscription(_ context.Context, polarSubscriptionID string) error {
	m.RevokedIDs = append(m.RevokedIDs, polarSubscriptionID)
	return m.RevokeErr
}
