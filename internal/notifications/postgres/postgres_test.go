package postgres

import (
	"context"
	"testing"
)

func TestNotifierRejectsNilQueries(t *testing.T) {
	if err := (&Notifier{}).Health(context.Background()); err == nil {
		t.Fatal("nil queries accepted")
	}
}
