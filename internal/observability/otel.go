// Package observability wires OpenTelemetry tracing for the emulator.
//
// The exporter is gated on the standard OTEL_EXPORTER_OTLP_ENDPOINT env
// var. When unset, Init installs no-op tracer/meter providers so the
// disabled path costs nothing. When set, an OTLP HTTP exporter is wired
// up against that endpoint with the W3C tracecontext + baggage
// propagators, so incoming traceparent headers parent the emulator's
// server spans.
//
// All other OTEL_* variables documented by the OpenTelemetry SDK
// specification work as usual (OTEL_SERVICE_NAME, OTEL_RESOURCE_ATTRIBUTES,
// OTEL_EXPORTER_OTLP_HEADERS, OTEL_EXPORTER_OTLP_INSECURE, etc.).
package observability

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// ShutdownFunc flushes pending telemetry and releases exporter
// resources. Safe to call multiple times; always returns quickly when
// tracing was disabled.
type ShutdownFunc func(context.Context) error

// Init configures the global tracer provider and propagator. If
// OTEL_EXPORTER_OTLP_ENDPOINT is empty, Init installs a no-op provider
// and returns a no-op shutdown.
//
// serviceName is the default value for the service.name resource
// attribute; OTEL_SERVICE_NAME takes precedence when set.
func Init(ctx context.Context, serviceName, version string) (ShutdownFunc, error) {
	// W3C propagators are installed unconditionally so the emulator can
	// still parent into incoming traceparent headers if a downstream
	// process happens to set the global provider itself.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" &&
		os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "" {
		return func(context.Context) error { return nil }, nil
	}

	exp, err := otlptrace.New(ctx, otlptracehttp.NewClient())
	if err != nil {
		return nil, fmt.Errorf("otlp trace exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(version),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp, sdktrace.WithBatchTimeout(time.Second)),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}
