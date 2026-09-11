// Package tracing initialises the OpenTelemetry trace pipeline (audit
// O11): stage-boundary spans over the OTLP/gRPC wire the deployed Tempo
// distributor already listens on. The standard OTel environment
// variables configure it — OTEL_EXPORTER_OTLP_ENDPOINT enables tracing
// (unset = noop tracer, zero cost), OTEL_SERVICE_NAME names the service
// (default "arbd"). Spans deliberately mark STAGE boundaries (a scanner
// pass, an evaluator tick, a paper cycle, a venue poll), never
// individual market-data messages: at thousands of messages per second
// per-message spans are noise, and log correlation by
// opportunity_id/cycle_id already carries the per-item story.
package tracing

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Enabled reports whether Init wired a real exporter (tests and the
// startup log).
var enabled bool

// Enabled reports whether spans are exported.
func Enabled() bool { return enabled }

// Init installs the global tracer provider. Without
// OTEL_EXPORTER_OTLP_ENDPOINT it installs the noop provider and returns
// a no-op shutdown — the platform runs exactly as before.
func Init(ctx context.Context, log *slog.Logger) (func(context.Context) error, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		otel.SetTracerProvider(noop.NewTracerProvider())
		log.Info("tracing disabled (set OTEL_EXPORTER_OTLP_ENDPOINT to enable)")
		return func(context.Context) error { return nil }, nil
	}

	exp, err := otlptracegrpc.New(ctx) // reads the standard OTLP env vars
	if err != nil {
		return nil, err
	}
	svc := os.Getenv("OTEL_SERVICE_NAME")
	if svc == "" {
		svc = "arbd"
	}
	res, err := sdkresource.New(ctx,
		sdkresource.WithFromEnv(), // honours OTEL_RESOURCE_ATTRIBUTES too
		sdkresource.WithAttributes(attribute.String("service.name", svc)),
	)
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		// Sampled: stage spans are low-rate by construction; parent-based
		// sampling keeps a future per-request decision in one place.
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(1.0))),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	enabled = true
	log.Info("tracing enabled", "endpoint", endpoint, "service", svc)
	return tp.Shutdown, nil
}

// Start opens a stage span. The noop provider (tracing disabled) makes
// this a cheap context pass-through, so call sites need no feature
// flag of their own.
func Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer("arb-chain-bot").Start(ctx, name, trace.WithAttributes(attrs...))
}
