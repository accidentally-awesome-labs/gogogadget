// Package otlp provides the generic OTLP target with concrete OpenTelemetry
// SDK providers. Export transport configuration is kept in this adapter so the
// seam remains independent of exporter/vendor packages.
package otlp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/telemetry"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type Provider struct {
	Tracer         *sdktrace.TracerProvider
	Meter          *sdkmetric.MeterProvider
	Endpoint       string
	Client         *http.Client
	spanExporter   *spanExporter
	metricExporter *metricExporter
}

func New(endpoint string) (*Provider, error) {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return nil, fmt.Errorf("otlp: endpoint is required")
	}
	client := &http.Client{Timeout: 2 * time.Second}
	spans := &spanExporter{endpoint: endpoint, client: client}
	metrics := &metricExporter{span: spans}
	return &Provider{
		Endpoint: endpoint, Client: client, spanExporter: spans, metricExporter: metrics,
		Tracer: sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithBatcher(spans)),
		Meter:  sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metrics))),
	}, nil
}
func (p *Provider) Providers(ctx context.Context) (telemetry.Providers, error) {
	if err := ctx.Err(); err != nil {
		return telemetry.Providers{}, err
	}
	if p == nil || p.Tracer == nil || p.Meter == nil {
		return telemetry.Providers{}, fmt.Errorf("otlp: providers are required")
	}
	return telemetry.Providers{Tracer: p.Tracer, Meter: p.Meter}, nil
}
func (p *Provider) Health(ctx context.Context) error {
	if p == nil || p.Endpoint == "" || p.Client == nil {
		return fmt.Errorf("otlp: endpoint and client are required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.Endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := p.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("otlp endpoint: %s", resp.Status)
	}
	return nil
}
func (p *Provider) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if err := p.Tracer.Shutdown(ctx); err != nil {
		return err
	}
	return p.Meter.Shutdown(ctx)
}

type Deps struct{ Endpoint string }
type Module struct {
	Value    telemetry.Providers
	provider *Provider
}

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	endpoint := d.Endpoint
	if endpoint == "" && h != nil {
		endpoint = h.Env("OTLP_ENDPOINT")
	}
	p, err := New(endpoint)
	if err != nil {
		return nil, err
	}
	v, err := p.Providers(ctx)
	if err != nil {
		return nil, err
	}
	return &Module{Value: v, provider: p}, nil
}
func (m *Module) Health(ctx context.Context) error {
	if m == nil || m.provider == nil {
		return fmt.Errorf("otlp: provider is required")
	}
	return m.provider.Health(ctx)
}
func (m *Module) Stop(ctx context.Context) error {
	if m == nil || m.provider == nil {
		return nil
	}
	return m.provider.Shutdown(ctx)
}
