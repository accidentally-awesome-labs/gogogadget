package otlp

import (
	"testing"
)

func TestNewRequiresEndpoint(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Fatal("missing endpoint accepted")
	}
}
