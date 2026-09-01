package redis

import (
	"testing"
)

func TestLimiterRequiresClient(t *testing.T) {
	if _, err := New(nil, 100); err == nil {
		t.Fatal("nil client accepted")
	}
}
