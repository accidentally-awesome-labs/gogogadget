package openai

import (
	"context"
	"testing"
)

func TestNewModuleRequiresEndpoint(t *testing.T) {
	_, err := NewModule(context.Background(), nil, Deps{})
	if err == nil {
		t.Fatal("missing endpoint accepted")
	}
}
