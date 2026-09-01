package ably

import (
	"context"
	"testing"
)

func TestModuleRejectsMissingSubscriber(t *testing.T) {
	_, err := NewModule(context.Background(), nil, Deps{Endpoint: "https://ably.test", APIKey: "key"})
	if err == nil {
		t.Fatal("missing subscriber accepted")
	}
}
