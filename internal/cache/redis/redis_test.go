package redis

import (
	"testing"
)

func TestStoreRequiresClient(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("nil client accepted")
	}
}
