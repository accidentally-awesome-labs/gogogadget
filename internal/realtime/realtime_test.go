package realtime

import (
	"context"
	"testing"
	"time"
)

func TestMemoryPublishSubscribeCopiesPayload(t *testing.T) {
	b := NewMemory()
	s, err := b.Subscribe(context.Background(), "topic")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := []byte("event")
	if err := b.Publish(context.Background(), "topic", p); err != nil {
		t.Fatal(err)
	}
	p[0] = 'X'
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := s.Next(ctx)
	if err != nil || string(got) != "event" {
		t.Fatalf("got %q err=%v", got, err)
	}
}
