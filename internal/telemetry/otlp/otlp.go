// Package otlp provides the generic OTLP target. Exporters are deliberately
// injected so the framework does not own vendor SDK lifecycle or credentials.
package otlp

import (
	"context"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/telemetry"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	"net/http"
	"time"
)

type Provider struct {
	Tracer   trace.TracerProvider
	Meter    metric.MeterProvider
	Endpoint string
	Client   *http.Client
}

func New(endpoint string, tracer trace.TracerProvider, meter metric.MeterProvider) *Provider {
	if tracer == nil {
		tracer = trace.NewNoopTracerProvider()
	}
	if meter == nil {
		meter = metricnoop.NewMeterProvider()
	}
	return &Provider{Endpoint: endpoint, Tracer: tracer, Meter: meter, Client: &http.Client{Timeout: 2 * time.Second}}
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
	if p.Endpoint == "" {
		return nil
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

type Deps struct{}
type Module struct{ Value telemetry.Providers }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	p := New(h.Env("OTLP_ENDPOINT"), nil, nil)
	v, e := p.Providers(ctx)
	return &Module{Value: v}, e
}

func (m *Module) Health(ctx context.Context) error { return nil }
