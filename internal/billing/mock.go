package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// MockClient is the billing test double — no HTTP mocking of the provider.
type MockClient struct {
	CheckoutURL string
	PortalURL   string
	CheckoutErr error
	PortalErr   error
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
	// The call is recorded before the injected failure: a checkout the
	// provider refused was still attempted, and a handler test asserting the
	// attempt needs to see it.
	if m.CheckoutErr != nil {
		return "", m.CheckoutErr
	}
	if m.CheckoutURL == "" {
		return "https://checkout.example.test/session", nil
	}
	return m.CheckoutURL, nil
}

func (m *MockClient) CreatePortalSession(_ context.Context, customerExternalID string) (string, error) {
	if m.PortalErr != nil {
		return "", m.PortalErr
	}
	if m.PortalURL == "" {
		return "https://portal.example.test/" + customerExternalID, nil
	}
	return m.PortalURL, nil
}

func (m *MockClient) RevokeSubscription(_ context.Context, providerSubscriptionID string) error {
	m.RevokedIDs = append(m.RevokedIDs, providerSubscriptionID)
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

// MockProvider is the provider key MockWebhook stamps on every event. It is
// deliberately neither a hosted adapter's key nor "local"/"dev", which the
// receiver refuses outright: a subscription row a double wrote must never be
// mistaken for one an adapter produced.
const MockProvider = "mock"

// MockDeliveryIDHeader carries the delivery id of a MockWebhook delivery.
// This double signs nothing, so it borrows no provider's signature header
// family; signature verification is each adapter's own contract test.
const MockDeliveryIDHeader = "Ggg-Mock-Delivery"

// MockWebhook is the BillingWebhook double, completing the pair with
// MockClient: a payload testing the neutral receiver — idempotency on the
// delivery id, the subscription state machine, the emails each transition
// enqueues — needs a webhook, and reaching for an adapter's pins a
// per-environment provider selection into a test that has no opinion about
// which provider is selected.
//
// The envelope is the neutral SubscriptionEvent itself, encoded by
// MockDelivery. The double owning both directions is the point: fixtures
// written by hand in an adapter's payload shape couple a payload to that
// adapter with no import for any check to see.
type MockWebhook struct {
	// Provider overrides the stamped provider key; empty means MockProvider.
	Provider string
	// Err, when non-nil, is what every delivery returns, standing in for a
	// signature an adapter refuses.
	Err error
}

// MockDelivery encodes one event as a delivery MockWebhook accepts. An empty
// deliveryID yields no header, which is how a payload drives the receiver's
// "missing webhook id" refusal.
func MockDelivery(deliveryID string, event SubscriptionEvent) ([]byte, http.Header, error) {
	payload, err := json.Marshal(event)
	if err != nil {
		return nil, nil, fmt.Errorf("mock billing delivery: %w", err)
	}
	headers := http.Header{}
	if deliveryID != "" {
		headers.Set(MockDeliveryIDHeader, deliveryID)
	}
	return payload, headers, nil
}

func (h MockWebhook) Verify(_ context.Context, payload []byte, headers http.Header) (SubscriptionEvent, error) {
	if h.Err != nil {
		return SubscriptionEvent{}, h.Err
	}
	var event SubscriptionEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return SubscriptionEvent{}, fmt.Errorf("mock billing webhook: %w", err)
	}
	if event.Type == "" {
		return SubscriptionEvent{}, fmt.Errorf("mock billing webhook: event type is required")
	}
	if strings.HasPrefix(event.Type, "subscription.") && event.ProviderSubscriptionID == "" {
		return SubscriptionEvent{}, fmt.Errorf("mock billing webhook: %s needs a subscription id", event.Type)
	}
	event.ID = headers.Get(MockDeliveryIDHeader)
	event.Provider = MockProvider
	if h.Provider != "" {
		event.Provider = h.Provider
	}
	return event, nil
}

var _ BillingWebhook = MockWebhook{}
