package billing

import (
	"context"
	"sync"
)

// MockClient is the billing test double — no HTTP mocking of the provider.
type MockClient struct {
	CheckoutURL string
	PortalURL   string
	RevokeErr   error
	IngestErr   error

	CheckoutCalls []CheckoutParams
	RevokedIDs    []string

	mu       sync.Mutex
	Ingested []struct {
		Customer string
		Events   []UsageEvent
	}
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

func (m *MockClient) IngestUsage(_ context.Context, customer string, events []UsageEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.IngestErr != nil {
		return m.IngestErr
	}
	m.Ingested = append(m.Ingested, struct {
		Customer string
		Events   []UsageEvent
	}{Customer: customer, Events: events})
	return nil
}
