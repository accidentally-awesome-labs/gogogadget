package postgres

import (
	"context"
	"github.com/gogogadget/gogogadget/internal/search"
	"testing"
)

func TestIndexRejectsNilDB(t *testing.T) {
	if err := (&Index{}).Upsert(context.Background(), search.Document{TenantID: "t", Collection: "c", ID: "d"}); err == nil {
		t.Fatal("nil db accepted")
	}
}
