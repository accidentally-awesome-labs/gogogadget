package postgres

import (
	"context"
	"github.com/gogogadget/gogogadget/internal/search"
	"testing"
)

func TestEmitterRejectsNilQueue(t *testing.T) {
	if err := (&Emitter{}).Health(context.Background()); err == nil {
		t.Fatal("nil queue accepted")
	}
}
