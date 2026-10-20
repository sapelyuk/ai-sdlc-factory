// Package telemetry configures OpenTelemetry tracing for the API server.
package telemetry

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const (
	// DefaultEndpoint is used when OTEL_EXPORTER_OTLP_ENDPOINT is not set.
	DefaultEndpoint = "otel-collector:4317"
	// shutdownTimeout bounds the flush of buffered spans on shutdown.
	shutdownTimeout = 5 * time.Second
)

// InitTracer configures the global OTel tracer provider with an OTLP/gRPC
// exporter and returns a function that flushes pending spans and stops it.
func InitTracer(ctx context.Context, serviceName string) (func(context.Context) error, error) {
	endpoint, insecure := exporterEndpoint()

	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(endpoint)}
	if insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}

	client := otlptracegrpc.NewClient(opts...)
	exporter, err := otlptrace.New(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("start OTLP trace exporter: %w", err)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(attribute.String("service.name", serviceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("build OTel resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	// Propagate W3C trace context (traceparent/tracestate) to downstream calls.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	shutdown := func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, shutdownTimeout)
		defer cancel()
		if err := tp.Shutdown(ctx); err != nil {
			return fmt.Errorf("shutdown tracer provider: %w", err)
		}
		return nil
	}
	return shutdown, nil
}

// exporterEndpoint resolves the OTLP/gRPC endpoint and whether the connection
// must be plaintext. The endpoint may be given as host:port or as a URL.
func exporterEndpoint() (endpoint string, insecure bool) {
	endpoint = os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}

	insecure = true
	switch {
	case strings.HasPrefix(endpoint, "https://"):
		insecure = false
		endpoint = strings.TrimPrefix(endpoint, "https://")
	case strings.HasPrefix(endpoint, "http://"):
		endpoint = strings.TrimPrefix(endpoint, "http://")
	}

	switch os.Getenv("OTEL_EXPORTER_OTLP_INSECURE") {
	case "true":
		insecure = true
	case "false":
		insecure = false
	}

	return endpoint, insecure
}
