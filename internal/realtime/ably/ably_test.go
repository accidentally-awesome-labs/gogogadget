package ably

import (
	"context"
	"testing"
)

func TestModuleConstructsConfiguredBroker(t *testing.T) {
	m, err := NewModule(context.Background(), nil, Deps{Endpoint: "https://ably.example", APIKey: "key"})
	if err != nil || m == nil || m.Value == nil {
		t.Fatalf("configured module: %v", err)
	}
	if err := m.Health(context.Background()); err != nil {
		t.Fatalf("health: %v", err)
	}
}
func TestModuleRejectsMissingCredentials(t *testing.T) {
	if _, err := NewModule(context.Background(), nil, Deps{}); err == nil {
		t.Fatal("missing credentials accepted")
	}
}
