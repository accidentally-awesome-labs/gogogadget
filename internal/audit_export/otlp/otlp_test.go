package otlp

import (
	"context"
	"testing"
)

func TestExporterRequiresEndpoint(t *testing.T) {
	if err := New("").Health(context.Background()); err == nil {
		t.Fatal("missing endpoint accepted")
	}
}
