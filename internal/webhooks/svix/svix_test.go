package svix

import (
	"context"
	"errors"
	"testing"
)

type queueFunc func(context.Context, string, string, any) error

func (f queueFunc) Enqueue(c context.Context, o, t string, d any) error { return f(c, o, t, d) }

func TestEnqueueFailureIsReportedAndSwallowed(t *testing.T) {
	want := errors.New("queue down")
	var reported error
	e := New(queueFunc(func(context.Context, string, string, any) error { return want }), nil, func(_ context.Context, err error) { reported = err })
	if err := e.Emit(context.Background(), "org", "event", map[string]any{"ok": true}); err != nil {
		t.Fatalf("Emit returned enqueue failure: %v", err)
	}
	if !errors.Is(reported, want) {
		t.Fatalf("reported = %v, want %v", reported, want)
	}
}
