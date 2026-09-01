package telemetry

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace"
)

type spanSink struct{ spans []trace.ReadOnlySpan }

func (s *spanSink) ExportSpans(_ context.Context, spans []trace.ReadOnlySpan) error {
	s.spans = append(s.spans, spans...)
	return nil
}
func (*spanSink) Shutdown(context.Context) error { return nil }
func TestCrossLayerWrappersCreateSpansAndRecordErrors(t *testing.T) {
	sink := &spanSink{}
	tp := trace.NewTracerProvider(trace.WithSyncer(sink))
	defer tp.Shutdown(context.Background())
	p := Providers{Tracer: tp}
	if err := PGX(context.Background(), p, "query", func(context.Context) error { return errors.New("db down") }); err == nil {
		t.Fatal("PGX swallowed error")
	}
	if err := Job(context.Background(), p, "search", func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := ProviderCall(context.Background(), p, "mail", func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(sink.spans) != 3 {
		t.Fatalf("spans=%d", len(sink.spans))
	}
}
