package tracing

import (
	"context"
	"testing"
	"time"
)

// Regression: Init merged resource.Default() (which carries the schema URL
// embedded in the OpenTelemetry SDK) with a resource pinned to a different
// semconv schema URL. resource.Merge rejects mismatched schema URLs, so Init
// returned "conflicting Schema URL" before ever setting tracerProvider/tracer/
// enabled — OTEL_TRACING_ENABLED=true silently produced no tracing at all.
// No network is involved: the OTLP HTTP exporter connects lazily.
func TestFixTracingInit_EnabledSucceeds(t *testing.T) {
	prevTracer := tracer
	prevProvider := tracerProvider
	prevEnabled := enabled
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = Shutdown(ctx)
		tracer = prevTracer
		tracerProvider = prevProvider
		enabled = prevEnabled
	})

	ctx := context.Background()
	cfg := Config{
		Enabled:     true,
		Endpoint:    "127.0.0.1:4318",
		Insecure:    true,
		SampleRate:  1.0,
		Environment: "test",
	}
	if err := Init(ctx, cfg); err != nil {
		t.Fatalf("Init(enabled) = %v, want nil", err)
	}
	if !IsEnabled() {
		t.Error("IsEnabled() = false after a successful Init")
	}
	if tracerProvider == nil {
		t.Error("tracerProvider is nil after a successful Init")
	}
	if Tracer() == nil {
		t.Error("Tracer() = nil after a successful Init")
	}
}
