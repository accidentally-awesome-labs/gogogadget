package noop

import (
	"context"
	"testing"
)

func TestNoopProvidersAreUsable(t *testing.T) {
	m, err := NewModule(context.Background(), nil, Deps{})
	if err != nil || m.Value.Tracer == nil || m.Value.Meter == nil {
		t.Fatalf("noop providers: %v", err)
	}
}
