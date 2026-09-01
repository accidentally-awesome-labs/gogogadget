package postgres

import (
	"context"
	"testing"
)

func TestServiceRejectsNilQueries(t *testing.T) {
	if err := (&Service{}).Health(context.Background()); err == nil {
		t.Fatal("nil queries accepted")
	}
}
