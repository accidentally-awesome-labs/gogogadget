package posthog

import (
	"testing"
)

func TestNewRequiresCredentials(t *testing.T) {
	if _, err := New("", ""); err == nil {
		t.Fatal("missing credentials accepted")
	}
}
