// Package telemetry exposes tracing and metrics providers independently of
// the observability error reporter.
package telemetry

import (
	"context"
	"fmt"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type Providers struct {
	Tracer trace.TracerProvider
	Meter  metric.MeterProvider
}
type Provider interface {
	Providers(context.Context) (Providers, error)
}

func Validate(p Providers) error {
	if p.Tracer == nil || p.Meter == nil {
		return fmt.Errorf("telemetry: tracer and meter providers are required")
	}
	return nil
}
