package search

import (
	"context"
	"testing"
)

func TestMemoryTenantAndFilterIsolation(t *testing.T) {
	s := NewMemory()
	if err := s.Upsert(context.Background(), Document{TenantID: "a", Collection: "docs", ID: "1", Text: "hello", Fields: map[string]string{"kind": "x"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(context.Background(), Document{TenantID: "b", Collection: "docs", ID: "2", Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	r, err := s.Query(context.Background(), Query{TenantID: "a", Collection: "docs", Text: "hello", Filters: map[string]string{"kind": "x"}})
	if err != nil || len(r.Hits) != 1 || r.Hits[0].ID != "1" {
		t.Fatalf("result=%+v err=%v", r, err)
	}
}
