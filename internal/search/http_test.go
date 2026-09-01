package search

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
)

type failingIndex struct{}

func (failingIndex) Upsert(context.Context, Document) error               { return nil }
func (failingIndex) Delete(context.Context, string, string, string) error { return nil }
func (failingIndex) Query(context.Context, Query) (Result, error) {
	return Result{}, errors.New("backend down")
}
func TestServeQueryReturns503OnProviderFailure(t *testing.T) {
	r := httptest.NewRequest("GET", "/search", nil)
	w := httptest.NewRecorder()
	ServeQuery(w, r, failingIndex{}, Query{TenantID: "t", Collection: "c"})
	if w.Code != 503 {
		t.Fatalf("status=%d", w.Code)
	}
}
