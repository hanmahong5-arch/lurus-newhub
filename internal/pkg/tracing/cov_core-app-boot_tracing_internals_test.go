package tracing

// cov_core-app-boot_tracing_internals_test.go — business-acceptance coverage
// for the tracing.go plumbing that the existing tracing_test.go leaves at 0%:
// Init's enabled path (real exporter/resource/sampler wiring), Shutdown,
// Tracer's global-vs-fallback selection, StartSpan/SpanFromContext/
// SetSpanAttributes/RecordError, and the getEnv* parsers. White-box (package
// tracing) so it can read/restore the unexported globals the same way
// tracing_test.go already does.

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// core_app_boot_resetTracingGlobals snapshots and restores the package
// globals mutated by Init/Shutdown/tests so ordering across the file (and
// against tracing_test.go, which also assigns `enabled` directly) stays
// independent.
func core_app_boot_resetTracingGlobals(t *testing.T) {
	t.Helper()
	prevTracer, prevProvider, prevEnabled := tracer, tracerProvider, enabled
	t.Cleanup(func() {
		tracer, tracerProvider, enabled = prevTracer, prevProvider, prevEnabled
	})
}

// core_app_boot_withRecorderTracer wires the global tracer to an in-memory
// SDK tracer backed by a SpanRecorder, without going through Init (so no
// real OTLP exporter / network is involved), and flips enabled=true.
func core_app_boot_withRecorderTracer(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	core_app_boot_resetTracingGlobals(t)
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tracer = tp.Tracer("cov-test")
	enabled = true
	return recorder
}

func core_app_boot_findSpan(recorder *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	for _, s := range recorder.Ended() {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

// TestCoreAppBootTracing_Init_EnabledWiresAllSamplerBranches exercises Init's
// enabled path across all three sampler branches (tracing.go:91-98):
// AlwaysSample, NeverSample and TraceIDRatioBased. fix_tracing_init_test.go
// only drives SampleRate=1.0, so without this the ratio and never branches go
// uncovered.
//
// Init used to fail unconditionally on a "conflicting Schema URL" from
// resource.Merge (the SDK's embedded semconv vs the explicitly pinned one), so
// OTEL_TRACING_ENABLED=true silently produced no tracing at all.
//
// No network is involved: the OTLP HTTP exporter connects lazily. Each case
// shuts the provider down again — a successful Init installs a live batch span
// processor on the package global, and leaving it running would leak a
// background goroutine into every later test in this package.
func TestCoreAppBootTracing_Init_EnabledWiresAllSamplerBranches(t *testing.T) {
	tests := []struct {
		name       string
		sampleRate float64
	}{
		{"always_sample", 1.5},
		{"never_sample", -1},
		{"ratio_sample", 0.25},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			core_app_boot_resetTracingGlobals(t)
			tracer, tracerProvider, enabled = nil, nil, false
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = Shutdown(ctx)
			})

			cfg := Config{
				Enabled:     true,
				Endpoint:    "127.0.0.1:1", // unroutable — exporter is lazy, no dial happens here
				Insecure:    true,
				SampleRate:  tc.sampleRate,
				Environment: "test",
			}
			if err := Init(context.Background(), cfg); err != nil {
				t.Fatalf("Init(SampleRate=%v) = %v, want nil", tc.sampleRate, err)
			}
			if !enabled {
				t.Error("enabled = false after a successful Init")
			}
			if tracer == nil {
				t.Error("tracer is nil after a successful Init")
			}
			if tracerProvider == nil {
				t.Error("tracerProvider is nil after a successful Init")
			}
			if !IsEnabled() {
				t.Error("IsEnabled() = false after a successful Init")
			}
		})
	}
}

// TestCoreAppBootTracing_Shutdown_NilProviderIsNoop locks the early-return
// branch: with no provider ever initialised, Shutdown must succeed
// immediately rather than panicking on a nil receiver.
func TestCoreAppBootTracing_Shutdown_NilProviderIsNoop(t *testing.T) {
	core_app_boot_resetTracingGlobals(t)
	tracerProvider = nil

	if err := Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown with nil tracerProvider should be a no-op returning nil, got %v", err)
	}
}

// TestCoreAppBootTracing_Tracer_FallbackAndGlobal covers both branches of
// Tracer(): falling back to the process-global otel tracer when the package
// tracer hasn't been initialised, and returning the exact wired tracer once
// it has.
func TestCoreAppBootTracing_Tracer_FallbackAndGlobal(t *testing.T) {
	t.Run("fallback_when_nil", func(t *testing.T) {
		core_app_boot_resetTracingGlobals(t)
		tracer = nil

		got := Tracer()
		if got == nil {
			t.Fatal("expected non-nil fallback tracer")
		}
		// Fallback tracer must still be usable to start spans without panicking.
		_, span := got.Start(context.Background(), "fallback-probe")
		span.End()
	})

	t.Run("returns_wired_global", func(t *testing.T) {
		recorder := core_app_boot_withRecorderTracer(t)
		got := Tracer()
		if got != tracer {
			t.Fatal("expected Tracer() to return the exact package-level tracer once set")
		}
		_, span := got.Start(context.Background(), "wired-probe")
		span.End()
		if core_app_boot_findSpan(recorder, "wired-probe") == nil {
			t.Fatal("expected the recorder wired via the global tracer to observe the span")
		}
	})
}

// TestCoreAppBootTracing_StartSpan_UsesGlobalTracer verifies StartSpan
// delegates to Tracer() (and thus to whatever recorder the global tracer is
// wired to), rather than always using a disconnected noop tracer.
func TestCoreAppBootTracing_StartSpan_UsesGlobalTracer(t *testing.T) {
	recorder := core_app_boot_withRecorderTracer(t)

	ctx, span := StartSpan(context.Background(), "cov-start-span")
	if !span.SpanContext().IsValid() {
		t.Fatal("expected a valid span context from a real recorder-backed tracer")
	}
	span.End()

	found := core_app_boot_findSpan(recorder, "cov-start-span")
	if found == nil {
		t.Fatal("expected recorder to capture the span started via StartSpan")
	}

	// SpanFromContext on the still-open (pre-End, freshly returned) ctx should
	// resolve back to the same span's trace ID.
	if sc := SpanFromContext(ctx).SpanContext(); sc.TraceID() != span.SpanContext().TraceID() {
		t.Errorf("SpanFromContext(ctx) trace ID mismatch: got %s want %s", sc.TraceID(), span.SpanContext().TraceID())
	}
}

// TestCoreAppBootTracing_SpanFromContext_NoActiveSpan covers the "nothing in
// context" branch — must return a non-recording noop span, not nil/panic.
func TestCoreAppBootTracing_SpanFromContext_NoActiveSpan(t *testing.T) {
	span := SpanFromContext(context.Background())
	if span == nil {
		t.Fatal("SpanFromContext must never return nil")
	}
	if span.SpanContext().IsValid() {
		t.Error("expected an invalid/noop span context when nothing was started")
	}
}

// TestCoreAppBootTracing_SetSpanAttributesAndRecordError_ActiveSpan asserts
// that attributes and errors set through the context-based helpers actually
// land on the real span recorded by the exporter — not just "didn't panic".
func TestCoreAppBootTracing_SetSpanAttributesAndRecordError_ActiveSpan(t *testing.T) {
	recorder := core_app_boot_withRecorderTracer(t)

	ctx, span := Tracer().Start(context.Background(), "cov-attrs-span")
	SetSpanAttributes(ctx, attribute.String("cov.key", "cov.value"))
	RecordError(ctx, errors.New("cov boom"))
	span.End()

	found := core_app_boot_findSpan(recorder, "cov-attrs-span")
	if found == nil {
		t.Fatal("expected span to be recorded")
	}
	var attrFound bool
	for _, a := range found.Attributes() {
		if a.Key == "cov.key" && a.Value.AsString() == "cov.value" {
			attrFound = true
		}
	}
	if !attrFound {
		t.Error("expected SetSpanAttributes to persist the attribute onto the real span")
	}
	if len(found.Events()) == 0 {
		t.Error("expected RecordError to add an error event onto the real span")
	}
}

// TestCoreAppBootTracing_SetSpanAttributesAndRecordError_NoActiveSpan covers
// the "no span in context" call sites (e.g. relay code called before
// tracing.Middleware runs) — must not panic and must simply be a no-op.
func TestCoreAppBootTracing_SetSpanAttributesAndRecordError_NoActiveSpan(t *testing.T) {
	ctx := context.Background()
	SetSpanAttributes(ctx, attribute.String("k", "v")) // no-op on the noop span; must not panic
	RecordError(ctx, errors.New("boom"))                // same

	// The noop span in this bare context must still report as non-recording —
	// proving the calls above didn't accidentally promote/attach real state.
	if SpanFromContext(ctx).IsRecording() {
		t.Error("expected the bare-context span to remain non-recording")
	}
}

// TestCoreAppBootTracing_GetEnvFloat covers every literal branch of the
// switch (default+unset, each recognised literal, and the unrecognised
// fallback) via a table so a regression in any single case is pinpointed.
func TestCoreAppBootTracing_GetEnvFloat(t *testing.T) {
	const key = "COV_CORE_APP_BOOT_TRACING_SAMPLE_RATE"
	tests := []struct {
		name    string
		envVal  string
		unset   bool
		want    float64
	}{
		{"unset_returns_default", "", true, 0.75},
		{"empty_string_returns_default", "", false, 0.75},
		{"zero", "0", false, 0},
		{"half", "0.5", false, 0.5},
		{"tenth", "0.1", false, 0.1},
		{"hundredth", "0.01", false, 0.01},
		{"unrecognised_falls_back_to_default", "0.33", false, 0.75},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Unsetenv(key)
			if !tc.unset {
				_ = os.Setenv(key, tc.envVal)
				t.Cleanup(func() { _ = os.Unsetenv(key) })
			}
			got := getEnvFloat(key, 0.75)
			if got != tc.want {
				t.Errorf("getEnvFloat(%q, 0.75) = %v, want %v", key, got, tc.want)
			}
		})
	}
}

// TestCoreAppBootTracing_GetEnvOrDefault covers both the set and unset
// branches.
func TestCoreAppBootTracing_GetEnvOrDefault(t *testing.T) {
	const key = "COV_CORE_APP_BOOT_TRACING_ENDPOINT"
	_ = os.Unsetenv(key)
	if got := getEnvOrDefault(key, "fallback-endpoint"); got != "fallback-endpoint" {
		t.Errorf("expected default when unset, got %q", got)
	}

	_ = os.Setenv(key, "custom-endpoint:4318")
	t.Cleanup(func() { _ = os.Unsetenv(key) })
	if got := getEnvOrDefault(key, "fallback-endpoint"); got != "custom-endpoint:4318" {
		t.Errorf("expected env override, got %q", got)
	}
}

// TestCoreAppBootTracing_StartLLMSpan_UsesGlobalTracer covers genai.go's
// StartLLMSpan (the Tracer()-backed wrapper around StartLLMSpanWithTracer,
// left uncovered by genai_test.go which only exercises the *WithTracer
// variant directly).
func TestCoreAppBootTracing_StartLLMSpan_UsesGlobalTracer(t *testing.T) {
	recorder := core_app_boot_withRecorderTracer(t)

	_, span := StartLLMSpan(context.Background(), "openai", "gpt-4o-mini")
	span.End()

	found := core_app_boot_findSpan(recorder, "gen_ai.chat")
	if found == nil {
		t.Fatal("expected StartLLMSpan to record a gen_ai.chat span via the global tracer")
	}
	var sawSystem, sawModel bool
	for _, a := range found.Attributes() {
		if string(a.Key) == "gen_ai.system" && a.Value.AsString() == "openai" {
			sawSystem = true
		}
		if string(a.Key) == "gen_ai.request.model" && a.Value.AsString() == "gpt-4o-mini" {
			sawModel = true
		}
	}
	if !sawSystem || !sawModel {
		t.Errorf("expected gen_ai.system=openai and gen_ai.request.model=gpt-4o-mini attributes, got %v", found.Attributes())
	}
}
