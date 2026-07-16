// Package telemetry wires OpenTelemetry tracing and Prometheus metrics for the
// Go services.
//
// The split is deliberate. Traces answer "what happened to this one request as
// it crossed services?" — they follow a single request through the gateway, the
// game-service, the session-manager, and the engine. Metrics answer "how is the
// system behaving in aggregate?" — rates, latencies, saturation. Q4's whole
// point is being able to ask both.
//
// Everything degrades: with no OTLP endpoint configured, tracing is a no-op and
// the services run exactly as before. Observability is never a dependency of
// playing chess.
package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Shutdown flushes and stops telemetry. Always safe to call.
type Shutdown func(context.Context) error

// InitTracing configures the global tracer to export spans over OTLP/gRPC.
//
// endpoint is host:port of an OTLP collector (Jaeger speaks OTLP directly). An
// empty endpoint installs a no-op: spans are still created by instrumented code,
// but nothing is exported and nothing is paid for.
func InitTracing(ctx context.Context, serviceName, version, endpoint string) (Shutdown, error) {
	// The W3C trace-context propagator is what carries a trace across a service
	// boundary: the gateway injects it into gRPC metadata, the game-service reads
	// it back, and the two spans join into one trace. Without it every service
	// would start its own disconnected trace.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	if endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(), // local collector, no TLS
	)
	if err != nil {
		return nil, fmt.Errorf("otlp trace exporter: %w", err)
	}

	// Merge onto Default() using a schema-less attribute set: passing our own
	// semconv SchemaURL here conflicts with the SDK's when the two track
	// different semconv versions, and resource.Merge then errors. The attribute
	// keys are stable across versions, so carrying no schema URL is safe.
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		attribute.String("service.name", serviceName),
		attribute.String("service.version", version),
	))
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		// Sample everything locally; a real deployment would sample a fraction of
		// high-volume traffic to bound cost.
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)

	return func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return tp.Shutdown(ctx)
	}, nil
}
