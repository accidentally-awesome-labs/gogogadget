package knock

import (
	"context"
	"errors"
	"testing"

	"github.com/gogogadget/gogogadget/internal/notifications"
)

type queueFunc func(context.Context, notifications.Message) error

func (f queueFunc) Enqueue(c context.Context, m notifications.Message) error { return f(c, m) }

func TestEnqueueFailureIsReportedAndSwallowed(t *testing.T) {
	want := errors.New("queue down")
	var reported error
	n := New(queueFunc(func(context.Context, notifications.Message) error { return want }), nil, func(_ context.Context, err error) { reported = err })
	if err := n.Send(context.Background(), "org", "user", "kind", "title", "body", "/url"); err != nil {
		t.Fatalf("Send returned enqueue failure: %v", err)
	}
	if !errors.Is(reported, want) {
		t.Fatalf("reported = %v, want %v", reported, want)
	}
}
