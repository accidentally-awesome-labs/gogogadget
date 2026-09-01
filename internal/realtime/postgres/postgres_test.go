package postgres

import (
	"context"
	"testing"
)

func TestBrokerRejectsNilDB(t *testing.T) {
	if err := (&Broker{}).Health(context.Background()); err == nil {
		t.Fatal("nil listener accepted")
	}
}
