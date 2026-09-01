package noop

import (
	"context"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/telemetry"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
)

type Provider struct{}

func New() Provider { return Provider{} }
func (Provider) Providers(ctx context.Context) (telemetry.Providers, error) {
	if err := ctx.Err(); err != nil {
		return telemetry.Providers{}, err
	}
	return telemetry.Providers{Tracer: trace.NewNoopTracerProvider(), Meter: metricnoop.NewMeterProvider()}, nil
}

type Deps struct{}
type Module struct{ Value telemetry.Providers }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	p, e := New().Providers(ctx)
	return &Module{Value: p}, e
}
func (m *Module) Health(ctx context.Context) error { return ctx.Err() }
