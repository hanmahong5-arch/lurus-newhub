// Package tracing provides OpenTelemetry distributed tracing for the API gateway.
package tracing

import (
	"context"
	"os"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	ServiceName    = "lurus-api"
	ServiceVersion = "1.0.0"
)

var (
	tracer         trace.Tracer
	tracerProvider *sdktrace.TracerProvider
	enabled        bool
)

// Config holds tracing configuration
type Config struct {
	Enabled      bool
	Endpoint     string // OTLP endpoint (e.g., "jaeger.lurus.cn:4318")
	Insecure     bool   // Use HTTP instead of HTTPS
	SampleRate   float64
	Environment  string
}

// LoadConfigFromEnv loads tracing configuration from environment variables
func LoadConfigFromEnv() Config {
	return Config{
		Enabled:     os.Getenv("OTEL_TRACING_ENABLED") == "true",
		Endpoint:    getEnvOrDefault("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4318"),
		Insecure:    os.Getenv("OTEL_EXPORTER_OTLP_INSECURE") != "false",
		SampleRate:  getEnvFloat("OTEL_TRACE_SAMPLE_RATE", 1.0),
		Environment: getEnvOrDefault("OTEL_ENVIRONMENT", "production"),
	}
}

// Init initializes the OpenTelemetry tracing system
func Init(ctx context.Context, cfg Config) error {
	if !cfg.Enabled {
		common.SysLog("OpenTelemetry tracing is disabled")
		enabled = false
		return nil
	}

	// Create OTLP HTTP exporter
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(cfg.Endpoint),
	}
	if cfg.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}

	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return err
	}

	// Create resource with service info.
	// The attribute set is intentionally schemaless: resource.Default() carries
	// the schema URL embedded in the SDK, and pinning a second (different)
	// schema URL here makes resource.Merge fail with a schema conflict, which
	// would abort Init and leave tracing silently off.
	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(
			semconv.ServiceName(ServiceName),
			semconv.ServiceVersion(ServiceVersion),
			attribute.String("environment", cfg.Environment),
		),
	)
	if err != nil {
		_ = exporter.Shutdown(ctx)
		return err
	}

	// Create sampler
	var sampler sdktrace.Sampler
	if cfg.SampleRate >= 1.0 {
		sampler = sdktrace.AlwaysSample()
	} else if cfg.SampleRate <= 0 {
		sampler = sdktrace.NeverSample()
	} else {
		sampler = sdktrace.TraceIDRatioBased(cfg.SampleRate)
	}

	// Create TracerProvider
	tracerProvider = sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(5*time.Second),
			sdktrace.WithMaxExportBatchSize(512),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	// Set global TracerProvider and Propagator
	otel.SetTracerProvider(tracerProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Get tracer instance
	tracer = tracerProvider.Tracer(ServiceName)
	enabled = true

	common.SysLog("OpenTelemetry tracing initialized")
	common.SysLog("OTLP endpoint: " + cfg.Endpoint)

	return nil
}

// Shutdown gracefully shuts down the tracing system
func Shutdown(ctx context.Context) error {
	if tracerProvider == nil {
		return nil
	}
	return tracerProvider.Shutdown(ctx)
}

// IsEnabled returns whether tracing is enabled
func IsEnabled() bool {
	return enabled
}

// Tracer returns the global tracer instance
func Tracer() trace.Tracer {
	if tracer == nil {
		return otel.Tracer(ServiceName)
	}
	return tracer
}

// StartSpan starts a new span with the given name
func StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return Tracer().Start(ctx, name, opts...)
}

// SpanFromContext returns the current span from context
func SpanFromContext(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}

// GetTraceID returns the trace ID from context as a string
func GetTraceID(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	if span == nil {
		return ""
	}
	sc := span.SpanContext()
	if !sc.HasTraceID() {
		return ""
	}
	return sc.TraceID().String()
}

// SetSpanAttributes sets attributes on the current span
func SetSpanAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	if span != nil {
		span.SetAttributes(attrs...)
	}
}

// RecordError records an error on the current span
func RecordError(ctx context.Context, err error) {
	span := trace.SpanFromContext(ctx)
	if span != nil {
		span.RecordError(err)
	}
}

// Helper functions
func getEnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func getEnvFloat(key string, defaultValue float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return defaultValue
	}
	// Simple parsing - could use strconv for more robustness
	switch v {
	case "0":
		return 0
	case "0.5":
		return 0.5
	case "0.1":
		return 0.1
	case "0.01":
		return 0.01
	default:
		return defaultValue
	}
}
