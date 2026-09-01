package noop

import (
	"context"
	"testing"
)

func TestNoopCapture(t *testing.T) {
	m, err := NewModule(context.Background(), nil, Deps{})
	if err != nil || m == nil {
		t.Fatalf("noop module: %v", err)
	}
	m.Capturer.Capture("u", "e", nil)
}
