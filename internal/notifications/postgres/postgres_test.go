package postgres

import (
	"context"
	"github.com/gogogadget/gogogadget/internal/search"
	"testing"
)

func TestNotifierRejectsNilQueries(t *testing.T) {
	if err := (&Notifier{}).Health(context.Background()); err == nil {
		t.Fatal("nil queries accepted")
	}
}
