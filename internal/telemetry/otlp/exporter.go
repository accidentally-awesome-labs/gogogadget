package otlp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/trace"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

type spanExporter struct {
	endpoint string
	client   *http.Client
	mu       sync.Mutex
	closed   bool
}

func (e *spanExporter) ExportSpans(ctx context.Context, spans []trace.ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}
	e.mu.Lock()
	closed := e.closed
	e.mu.Unlock()
	if closed {
		return fmt.Errorf("otlp: span exporter is shut down")
	}
	items := make([]*tracepb.Span, 0, len(spans))
	for _, span := range spans {
		attrs := make([]*commonpb.KeyValue, 0, len(span.Attributes()))
		for _, a := range span.Attributes() {
			attrs = append(attrs, &commonpb.KeyValue{Key: string(a.Key), Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: a.Value.AsString()}}})
		}
		tid := span.SpanContext().TraceID()
		sid := span.SpanContext().SpanID()
		items = append(items, &tracepb.Span{TraceId: tid[:], SpanId: sid[:], Name: span.Name(), StartTimeUnixNano: uint64(span.StartTime().UnixNano()), EndTimeUnixNano: uint64(span.EndTime().UnixNano()), Attributes: attrs})
	}
	payload, err := proto.Marshal(&collectortrace.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{Resource: &resourcepb.Resource{}, ScopeSpans: []*tracepb.ScopeSpans{{Spans: items}}}}})
	if err != nil {
		return err
	}
	return e.postProto(ctx, "/v1/traces", payload)
}
func (e *spanExporter) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	e.closed = true
	e.mu.Unlock()
	return nil
}
func (e *spanExporter) postProto(ctx context.Context, suffix string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint+suffix, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("otlp: exporter returned %s", resp.Status)
	}
	return nil
}
func (e *spanExporter) post(ctx context.Context, suffix string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint+suffix, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("otlp: exporter returned %s", resp.Status)
	}
	return nil
}

type metricExporter struct{ span *spanExporter }

func (e *metricExporter) Temporality(metric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}
func (e *metricExporter) Aggregation(k metric.InstrumentKind) metric.Aggregation {
	return metric.DefaultAggregationSelector(k)
}
func (e *metricExporter) Export(ctx context.Context, data *metricdata.ResourceMetrics) error {
	if data == nil {
		return nil
	}
	return e.span.post(ctx, "/v1/metrics", data)
}
func (e *metricExporter) ForceFlush(context.Context) error   { return nil }
func (e *metricExporter) Shutdown(ctx context.Context) error { return e.span.Shutdown(ctx) }
