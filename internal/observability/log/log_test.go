package log

import (
	"context"
	"testing"
)

func TestLoggerHealth(t *testing.T) {
	m, err := NewModule(context.Background(), nil, Deps{})
	if err != nil || m.Health(context.Background()) != nil {
		t.Fatalf("logger health: %v", err)
	}
}
