package noop

import (
	"context"
	"github.com/gogogadget/gogogadget/internal/audit"
	"testing"
)

func TestExporterIsNoop(t *testing.T) {
	if err := New().Export(context.Background(), audit.Entry{Action: "x"}); err != nil {
		t.Fatal(err)
	}
}
