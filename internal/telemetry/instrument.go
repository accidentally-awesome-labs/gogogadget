package telemetry

import (
	"context"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Start starts a span when a provider is available. This keeps instrumentation
// optional for local/noop adapters without forcing callers to branch.
func Start(ctx context.Context, providers Providers, name string) (context.Context, trace.Span) {
	if providers.Tracer == nil {
		return ctx, trace.SpanFromContext(ctx)
	}
	return providers.Tracer.Tracer(name).Start(ctx, name)
}
func RecordError(span trace.Span, err error) {
	if span != nil && err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
}

// HTTP wraps an entire request, recording method/route/status and duration.
func HTTP(next http.Handler, providers Providers, service string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := Start(r.Context(), providers, service+" "+r.Method)
		defer span.End()
		start := time.Now()
		w.Header().Set("Server-Timing", "otel;dur=0")
		next.ServeHTTP(w, r.WithContext(ctx))
		span.SetAttributes(attribute.String("http.method", r.Method), attribute.String("http.route", r.URL.Path), attribute.Int64("http.duration_ms", time.Since(start).Milliseconds()))
	})
}

// Call instruments provider/database/job calls with the same error semantics.
func Call(ctx context.Context, providers Providers, name string, fn func(context.Context) error) error {
	ctx, span := Start(ctx, providers, name)
	defer span.End()
	err := fn(ctx)
	RecordError(span, err)
	return err
}
