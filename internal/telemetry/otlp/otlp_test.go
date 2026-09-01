package otlp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

type captureExporter struct{ exporter *spanExporter }

func (c captureExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	return c.exporter.ExportSpans(ctx, spans)
}
func (c captureExporter) Shutdown(ctx context.Context) error { return c.exporter.Shutdown(ctx) }

func TestSpanExporterUsesOTLPProtobuf(t *testing.T) {
	bodyCh := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "application/x-protobuf" {
			t.Errorf("content type=%q", got)
		}
		b, _ := io.ReadAll(r.Body)
		bodyCh <- b
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	exp := &spanExporter{endpoint: srv.URL, client: srv.Client()}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(captureExporter{exporter: exp}))
	tr := tp.Tracer("contract")
	_, span := tr.Start(context.Background(), "protocol-span")
	span.End()
	if err := tp.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-bodyCh:
		var msg collectortrace.ExportTraceServiceRequest
		if err := proto.Unmarshal(raw, &msg); err != nil {
			t.Fatal(err)
		}
		if len(msg.ResourceSpans) == 0 || len(msg.ResourceSpans[0].ScopeSpans) == 0 || msg.ResourceSpans[0].ScopeSpans[0].Spans[0].Name != "protocol-span" {
			t.Fatalf("decoded OTLP=%v", msg)
		}
	}
}

func TestNewRequiresEndpoint(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Fatal("missing endpoint accepted")
	}
}
