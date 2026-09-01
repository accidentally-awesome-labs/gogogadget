package postgres

import (
	"testing"
)

func TestPoolAccessorNilSafe(t *testing.T) {
	if Pool(nil) != nil || Queries(nil) != nil {
		t.Fatal("nil accessor returned value")
	}
}
