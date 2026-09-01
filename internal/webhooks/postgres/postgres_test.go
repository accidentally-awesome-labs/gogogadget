package postgres

import (
	"context"
	"testing"
)

func TestEmitterRejectsNilQueue(t *testing.T) {
	if err := (&Emitter{}).Health(context.Background()); err == nil {
		t.Fatal("nil queue accepted")
	}
}
