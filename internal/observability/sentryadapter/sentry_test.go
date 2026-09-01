package sentryadapter

import (
	"context"
	"testing"
)

func TestNewRequiresDSN(t *testing.T) {
	_, err := NewModule(context.Background(), nil, Deps{})
	if err == nil {
		t.Fatal("missing dsn accepted")
	}
}
