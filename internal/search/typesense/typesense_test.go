package typesense

import (
	"context"
	"testing"
)

func TestIndexRejectsMissingEndpoint(t *testing.T) {
	if err := (&Index{}).Health(context.Background()); err == nil {
		t.Fatal("missing endpoint accepted")
	}
}
