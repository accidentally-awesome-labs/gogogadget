package postgres

import (
	"context"
	"github.com/gogogadget/gogogadget/internal/search"
	"testing"
)

func TestRecorderRejectsNilQueries(t *testing.T) {
	if err := (&Recorder{}).Health(context.Background()); err == nil {
		t.Fatal("nil queries accepted")
	}
}
