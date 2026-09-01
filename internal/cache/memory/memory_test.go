package memory

import (
	"context"
	"testing"
	"time"
)

func TestStoreCopiesAndExpires(t *testing.T) {
	s := New()
	v := []byte("value")
	if err := s.Set(context.Background(), "x", v, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	v[0] = 'X'
	got, ok, err := s.Get(context.Background(), "x")
	if err != nil || !ok || string(got) != "value" {
		t.Fatalf("got %q %v %v", got, ok, err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, ok, _ := s.Get(context.Background(), "x"); ok {
		t.Fatal("expired value returned")
	}
}
