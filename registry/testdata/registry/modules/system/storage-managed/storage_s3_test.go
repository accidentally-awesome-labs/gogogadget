package storages3

import (
	"bytes"
	"context"
	"testing"
)

func TestExampleStorageS3Stores(t *testing.T) {
	n, err := (store{}).Put(context.Background(), "fixture", "text/plain", bytes.NewBufferString("ok"))
	if err != nil || n != 2 {
		t.Fatalf("Put = (%d, %v), want (2, nil)", n, err)
	}
}
