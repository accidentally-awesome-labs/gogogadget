package openmeter

import (
	"context"
	"errors"
	"testing"
)

type queueFunc func(context.Context, string, string, int64, string, map[string]any) error

func (f queueFunc) Enqueue(c context.Context, o, n string, v int64, e string, m map[string]any) error {
	return f(c, o, n, v, e, m)
}

func TestEnqueueFailureIsReportedAndSwallowed(t *testing.T) {
	want := errors.New("queue down")
	var reported error
	r := New(queueFunc(func(context.Context, string, string, int64, string, map[string]any) error { return want }), nil, func(_ context.Context, err error) { reported = err })
	if err := r.Record(context.Background(), "org", "requests", 1, "", nil); err != nil {
		t.Fatalf("Record returned enqueue failure: %v", err)
	}
	if !errors.Is(reported, want) {
		t.Fatalf("reported = %v, want %v", reported, want)
	}
}
