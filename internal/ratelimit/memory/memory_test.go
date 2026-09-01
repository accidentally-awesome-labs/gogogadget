package memory

import (
	"context"
	"testing"
)

func TestLimiterReportsDecision(t *testing.T) {
	l := New(1, 1)
	d, err := l.Allow(context.Background(), "key")
	if err != nil || !d.Allowed || d.Limit != 1 {
		t.Fatalf("decision=%+v err=%v", d, err)
	}
	d, err = l.Allow(context.Background(), "key")
	if err != nil || d.Allowed || d.RetryAfter <= 0 {
		t.Fatalf("second decision=%+v err=%v", d, err)
	}
}
