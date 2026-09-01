package billing

import (
	"context"
	"net/http"
	"testing"
)

type testWebhook struct{}

func (testWebhook) Verify(context.Context, []byte, http.Header) (SubscriptionEvent, error) {
	return SubscriptionEvent{}, nil
}

func TestNewModuleRequiresCompleteCapabilities(t *testing.T) {
	_, err := NewModule(context.Background(), Deps{})
	if err == nil {
		t.Fatal("NewModule(empty) = nil error, want refusal")
	}
	catalog := DefaultPlanCatalog()
	m, err := NewModule(context.Background(), Deps{Client: &MockClient{}, Catalog: catalog, Webhook: testWebhook{}})
	if err != nil {
		t.Fatalf("NewModule(complete): %v", err)
	}
	if m.Client == nil || m.Catalog == nil || m.Webhook == nil {
		t.Fatal("complete capability set was not retained")
	}
}
