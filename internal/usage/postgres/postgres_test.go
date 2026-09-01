package postgres

import (
	"context"
	"testing"
)

func TestRecorderRejectsNilQueries(t *testing.T) {
	if err := (&Recorder{}).Health(context.Background()); err == nil {
		t.Fatal("nil queries accepted")
	}
}
