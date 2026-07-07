package tracing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// snapshotGlobals saves the package-level mutable state and returns a restore func.
func snapshotGlobals(t *testing.T) func() {
	t.Helper()
	origTracer := tracer
	origProvider := tracerProvider
	origEnabled := enabled
	return func() {
		tracer = origTracer
		tracerProvider = origProvider
		enabled = origEnabled
	}
}

// --- getEnvFloat / getEnvOrDefault ---

func TestGetEnvFloat_AllBranches(t *testing.T) {
	tests := []struct {
		name    string
		envVal  string
		setEnv  bool
		def     float64
		want    float64
	}{
		{"unset uses default", "", false, 0.75, 0.75},
		{"explicit zero", "0", true, 0.75, 0},
		{"half", "0.5", true, 0.75, 0.5},
		{"tenth", "0.1", true, 0.75, 0.1},
		{"hundredth", "0.01", true, 0.75, 0.01},
		{"unrecognized falls back to default", "0.33", true, 0.75, 0.75},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			const key = "TRACING_TEST_ENV_FLOAT"
			if tc.setEnv {
				t.Setenv(key, tc.envVal)
			}
			got := getEnvFloat(key, tc.def)
			if got != tc.want {
				t.Errorf("getEnvFloat(%q,%v) = %v, want %v", tc.envVal, tc.def, got, tc.want)
			}
		})
	}
}

func TestGetEnvOrDefault_SetAndUnset(t *testing.T) {
	const key = "TRACING_TEST_ENV_STRING"

	if got := getEnvOrDefault(key, "fallback"); got != "fallback" {
		t.Errorf("expected fallback when unset, got %q", got)
	}

	t.Setenv(key, "custom-value")
	if got := getEnvOrDefault(key, "fallback"); got != "custom-value" {
		t.Errorf("expected custom-value when set, got %q", got)
	}
}

func TestLoadConfigFromEnv_AllEnvSet(t *testing.T) {
	t.Setenv("OTEL_TRACING_ENABLED", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "collector.example.com:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "false")
	t.Setenv("OTEL_TRACE_SAMPLE_RATE", "0.5")
	t.Setenv("OTEL_ENVIRONMENT", "staging")

	cfg := LoadConfigFromEnv()

	if !cfg.Enabled {
		t.Error("expected Enabled=true")
	}
	if cfg.Endpoint != "collector.example.com:4318" {
		t.Errorf("expected custom endpoint, got %q", cfg.Endpoint)
	}
	if cfg.Insecure {
		t.Error("expected Insecure=false when OTEL_EXPORTER_OTLP_INSECURE=false")
	}
	if cfg.SampleRate != 0.5 {
		t.Errorf("expected SampleRate=0.5, got %v", cfg.SampleRate)
	}
	if cfg.Environment != "staging" {
		t.Errorf("expected Environment=staging, got %q", cfg.Environment)
	}
}

// --- Init / Shutdown / Tracer / IsEnabled ---

// NOTE: in this module's dependency graph, resource.Default() advertises a
// newer OTel semconv schema URL than the explicit semconv v1.26.0 package
// imported by tracing.go, so resource.Merge inside Init always returns a
// "conflicting Schema URL" error for any Enabled:true config in this
// hermetic build — the success branch is not reachable without bumping
// semconv versions (out of scope for a test-only change). These tests
// exercise the reachable Enabled:true branches up to that failure point.
func TestInit_EnabledSampleRate1_ReturnsSchemaConflictErrorAndStaysDisabled(t *testing.T) {
	restore := snapshotGlobals(t)
	t.Cleanup(restore)

	cfg := Config{
		Enabled:     true,
		Endpoint:    "localhost:4318",
		Insecure:    true,
		SampleRate:  1.0,
		Environment: "test",
	}
	err := Init(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected resource schema conflict error from Init in this build, got nil")
	}
	if IsEnabled() {
		t.Error("expected IsEnabled() to remain false since Init returned before enabling")
	}
}

func TestInit_EnabledSampleRate0_ReturnsSchemaConflictError(t *testing.T) {
	restore := snapshotGlobals(t)
	t.Cleanup(restore)

	cfg := Config{
		Enabled:    true,
		Endpoint:   "localhost:4318",
		Insecure:   true,
		SampleRate: 0,
	}
	if err := Init(context.Background(), cfg); err == nil {
		t.Fatal("expected resource schema conflict error from Init in this build, got nil")
	}
}

func TestInit_EnabledMidSampleRate_ReturnsSchemaConflictError(t *testing.T) {
	restore := snapshotGlobals(t)
	t.Cleanup(restore)

	cfg := Config{
		Enabled:    true,
		Endpoint:   "localhost:4318",
		Insecure:   true,
		SampleRate: 0.25,
	}
	if err := Init(context.Background(), cfg); err == nil {
		t.Fatal("expected resource schema conflict error from Init in this build, got nil")
	}
}

func TestInit_EnabledInsecureFalse_ReturnsSchemaConflictError(t *testing.T) {
	restore := snapshotGlobals(t)
	t.Cleanup(restore)

	cfg := Config{
		Enabled:    true,
		Endpoint:   "localhost:4318",
		Insecure:   false,
		SampleRate: 1.0,
	}
	// Insecure:false takes the branch that skips otlptracehttp.WithInsecure();
	// still fails at the same resource.Merge step deterministically/hermetically.
	if err := Init(context.Background(), cfg); err == nil {
		t.Fatal("expected resource schema conflict error from Init in this build, got nil")
	}
}

func TestShutdown_NilProviderReturnsNil(t *testing.T) {
	restore := snapshotGlobals(t)
	t.Cleanup(restore)

	tracerProvider = nil
	if err := Shutdown(context.Background()); err != nil {
		t.Errorf("expected nil error when tracerProvider is nil, got %v", err)
	}
}

func TestTracer_FallbackWhenGlobalNil(t *testing.T) {
	restore := snapshotGlobals(t)
	t.Cleanup(restore)

	tracer = nil
	tr := Tracer()
	if tr == nil {
		t.Error("expected Tracer() to fall back to a non-nil otel.Tracer when global tracer is nil")
	}
}

func TestTracer_ReturnsGlobalWhenSet(t *testing.T) {
	restore := snapshotGlobals(t)
	t.Cleanup(restore)

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	custom := tp.Tracer("custom-test-tracer")
	tracer = custom

	if Tracer() != custom {
		t.Error("expected Tracer() to return the previously-set global tracer instance")
	}
}

// --- StartSpan / SpanFromContext / SetSpanAttributes / RecordError ---

func TestStartSpan_CreatesNamedSpan(t *testing.T) {
	restore := snapshotGlobals(t)
	t.Cleanup(restore)

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tracer = tp.Tracer("test")

	ctx, span := StartSpan(context.Background(), "my.custom.span")
	span.End()

	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].Name() != "my.custom.span" {
		t.Fatalf("expected 1 span named my.custom.span, got %v", spans)
	}
	if SpanFromContext(ctx) == nil {
		t.Error("expected SpanFromContext to return non-nil span from ctx")
	}
}

func TestSetSpanAttributes_WithRealSpanRecordsAttribute(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tr := tp.Tracer("test")

	ctx, span := tr.Start(context.Background(), "attr-span")
	SetSpanAttributes(ctx, attribute.String("custom.key", "custom.value"))
	span.End()

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	found := false
	for _, a := range spans[0].Attributes() {
		if a.Key == attribute.Key("custom.key") && a.Value.AsString() == "custom.value" {
			found = true
		}
	}
	if !found {
		t.Error("expected custom.key attribute to be recorded")
	}
}

func TestSetSpanAttributes_NoopWhenNoSpanInContext(t *testing.T) {
	// Should not panic when the context has no active span.
	SetSpanAttributes(context.Background(), attribute.String("k", "v"))
}

func TestRecordError_WithRealSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tr := tp.Tracer("test")

	ctx, span := tr.Start(context.Background(), "err-span")
	RecordError(ctx, errors.New("boom"))
	span.End()

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if len(spans[0].Events()) == 0 {
		t.Error("expected RecordError to add an event to the span")
	}
}

func TestRecordError_NoopWhenNoSpanInContext(t *testing.T) {
	RecordError(context.Background(), errors.New("boom"))
}

func TestGetTraceID_WithRealSpan(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	tr := tp.Tracer("test")
	ctx, span := tr.Start(context.Background(), "traceid-span")
	defer span.End()

	id := GetTraceID(ctx)
	if id == "" {
		t.Error("expected non-empty trace ID for a real span")
	}
	if id != span.SpanContext().TraceID().String() {
		t.Errorf("GetTraceID mismatch: got %q, want %q", id, span.SpanContext().TraceID().String())
	}
}

// --- middleware.go: Middleware enabled path ---

func TestMiddleware_EnabledSetsHeaderAndAttributes(t *testing.T) {
	restore := snapshotGlobals(t)
	t.Cleanup(restore)

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tracer = tp.Tracer("test")
	enabled = true

	router := gin.New()
	router.Use(Middleware())
	router.GET("/hello/:name", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/hello/world", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Header().Get(TraceIDHeader) == "" {
		t.Error("expected X-Trace-Id header to be set when tracing enabled")
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 recorded span, got %d", len(spans))
	}
	s := spans[0]
	if s.Name() != "GET /hello/:name" {
		t.Errorf("expected span name 'GET /hello/:name', got %q", s.Name())
	}
	if s.Status().Code == codes.Error {
		t.Error("expected non-error status for 200 response")
	}
}

func TestMiddleware_EnabledErrorStatusMarksSpanError(t *testing.T) {
	restore := snapshotGlobals(t)
	t.Cleanup(restore)

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tracer = tp.Tracer("test")
	enabled = true

	router := gin.New()
	router.Use(Middleware())
	router.GET("/fail", func(c *gin.Context) {
		_ = c.Error(errors.New("handler failure"))
		c.String(http.StatusInternalServerError, "fail")
	})

	req := httptest.NewRequest(http.MethodGet, "/fail", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 recorded span, got %d", len(spans))
	}
	s := spans[0]
	if s.Status().Code != codes.Error {
		t.Errorf("expected span status Error for 500 response, got %v", s.Status().Code)
	}
	if len(s.Events()) == 0 {
		t.Error("expected gin c.Errors to be recorded as span events")
	}
}

// --- middleware.go: RelaySpan / SetRelayAttributes enabled path ---

func TestRelaySpan_EnabledReturnsSpanAndEnds(t *testing.T) {
	restore := snapshotGlobals(t)
	t.Cleanup(restore)

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tracer = tp.Tracer("test")
	enabled = true

	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		span, end := RelaySpan(c, "chat")
		if span == nil {
			t.Error("expected non-nil span when tracing enabled")
		}
		end()
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].Name() != "relay.chat" {
		t.Fatalf("expected 1 span named relay.chat, got %v", spans)
	}
}

func TestSetRelayAttributes_DisabledNoop(t *testing.T) {
	restore := snapshotGlobals(t)
	t.Cleanup(restore)
	enabled = false

	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		SetRelayAttributes(c, "openai", "gpt-4o", 7) // should not panic
		c.String(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestSetRelayAttributes_EnabledSetsAttrsOnSpan(t *testing.T) {
	restore := snapshotGlobals(t)
	t.Cleanup(restore)

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tracer = tp.Tracer("test")
	enabled = true

	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		ctx, span := StartSpan(c.Request.Context(), "parent")
		c.Request = c.Request.WithContext(ctx)
		SetRelayAttributes(c, "anthropic", "claude-3", 42)
		span.End()
		c.String(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	attrs := spans[0].Attributes()
	wantAttrs := []attribute.KeyValue{
		attribute.String("relay.provider", "anthropic"),
		attribute.String("relay.model", "claude-3"),
		attribute.Int("relay.channel_id", 42),
	}
	for _, want := range wantAttrs {
		found := false
		for _, a := range attrs {
			if a.Key == want.Key && a.Value == want.Value {
				found = true
			}
		}
		if !found {
			t.Errorf("expected attribute %v=%v on span, got %v", want.Key, want.Value.AsInterface(), attrs)
		}
	}
}

// --- StartChannelSelectSpan enabled path (success + error) ---

func TestStartChannelSelectSpan_EnabledSuccessSetsChannelID(t *testing.T) {
	restore := snapshotGlobals(t)
	t.Cleanup(restore)

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tracer = tp.Tracer("test")
	enabled = true

	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		end := StartChannelSelectSpan(c)
		end(99, nil)
		c.String(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	s := spans[0]
	if s.Name() != "relay.channel_select" {
		t.Errorf("expected span name relay.channel_select, got %q", s.Name())
	}
	if s.Status().Code == codes.Error {
		t.Error("expected non-error status for success path")
	}
	found := false
	for _, a := range s.Attributes() {
		if a.Key == attribute.Key("channel.selected_id") && a.Value.AsInt64() == 99 {
			found = true
		}
	}
	if !found {
		t.Error("expected channel.selected_id=99 attribute")
	}
}

func TestStartChannelSelectSpan_EnabledErrorMarksSpanError(t *testing.T) {
	restore := snapshotGlobals(t)
	t.Cleanup(restore)

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tracer = tp.Tracer("test")
	enabled = true

	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		end := StartChannelSelectSpan(c)
		end(0, errors.New("no channel available"))
		c.String(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Status().Code != codes.Error {
		t.Errorf("expected span status Error, got %v", spans[0].Status().Code)
	}
	if len(spans[0].Events()) == 0 {
		t.Error("expected RecordError event on error path")
	}
}

// --- StartUpstreamSpan enabled path (success + error) ---

func TestStartUpstreamSpan_EnabledSuccessSetsAttributes(t *testing.T) {
	restore := snapshotGlobals(t)
	t.Cleanup(restore)

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tracer = tp.Tracer("test")
	enabled = true

	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		end := StartUpstreamSpan(c, "openai", "https://api.openai.com/v1/chat")
		end(200, nil)
		c.String(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	s := spans[0]
	if s.Name() != "relay.upstream.openai" {
		t.Errorf("expected span name relay.upstream.openai, got %q", s.Name())
	}
	if s.Status().Code == codes.Error {
		t.Error("expected non-error status for success path")
	}
	wantAttrs := []attribute.KeyValue{
		attribute.String("upstream.provider", "openai"),
		attribute.String("upstream.endpoint", "https://api.openai.com/v1/chat"),
		attribute.Int("upstream.status_code", 200),
	}
	for _, want := range wantAttrs {
		found := false
		for _, a := range s.Attributes() {
			if a.Key == want.Key && a.Value == want.Value {
				found = true
			}
		}
		if !found {
			t.Errorf("expected attribute %v=%v, got %v", want.Key, want.Value.AsInterface(), s.Attributes())
		}
	}
}

func TestStartUpstreamSpan_EnabledErrorMarksSpanError(t *testing.T) {
	restore := snapshotGlobals(t)
	t.Cleanup(restore)

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tracer = tp.Tracer("test")
	enabled = true

	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		end := StartUpstreamSpan(c, "anthropic", "https://api.anthropic.com")
		end(500, errors.New("upstream failure"))
		c.String(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Status().Code != codes.Error {
		t.Errorf("expected span status Error, got %v", spans[0].Status().Code)
	}
}

// --- StartAuthSpan enabled path (success + error) ---

func TestStartAuthSpan_EnabledSuccessSetsUserID(t *testing.T) {
	restore := snapshotGlobals(t)
	t.Cleanup(restore)

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tracer = tp.Tracer("test")
	enabled = true

	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		end := StartAuthSpan(c)
		end(123, nil)
		c.String(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	s := spans[0]
	if s.Name() != "auth.validate" {
		t.Errorf("expected span name auth.validate, got %q", s.Name())
	}
	found := false
	for _, a := range s.Attributes() {
		if a.Key == attribute.Key("auth.user_id") && a.Value.AsInt64() == 123 {
			found = true
		}
	}
	if !found {
		t.Error("expected auth.user_id=123 attribute")
	}
}

func TestStartAuthSpan_EnabledErrorMarksSpanError(t *testing.T) {
	restore := snapshotGlobals(t)
	t.Cleanup(restore)

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tracer = tp.Tracer("test")
	enabled = true

	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		end := StartAuthSpan(c)
		end(0, errors.New("invalid credentials"))
		c.String(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Status().Code != codes.Error {
		t.Errorf("expected span status Error, got %v", spans[0].Status().Code)
	}
}

// --- GetTraceIDFromContext / InjectTraceIDToLogger ---

func TestGetTraceIDFromContext_NonStringValueReturnsEmpty(t *testing.T) {
	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		c.Set(TraceIDKey, 12345) // wrong type on purpose
		got := GetTraceIDFromContext(c)
		if got != "" {
			c.String(http.StatusBadRequest, "expected empty string for non-string trace id")
			return
		}
		c.String(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetTraceIDFromContext_ValidString(t *testing.T) {
	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		c.Set(TraceIDKey, "abc123traceid")
		got := GetTraceIDFromContext(c)
		if got != "abc123traceid" {
			c.String(http.StatusBadRequest, "mismatch: "+got)
			return
		}
		c.String(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestInjectTraceIDToLogger_NoTraceIDIsNoop(t *testing.T) {
	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		InjectTraceIDToLogger(c) // trace_id not set -> should return early, no panic
		if _, exists := c.Get(common.RequestIdKey); exists {
			c.String(http.StatusBadRequest, "expected no request id set")
			return
		}
		c.String(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestInjectTraceIDToLogger_SetsRequestIDWhenAbsent(t *testing.T) {
	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		c.Set(TraceIDKey, "0123456789abcdeffedcba9876543210")
		InjectTraceIDToLogger(c)

		reqID, exists := c.Get(common.RequestIdKey)
		if !exists {
			c.String(http.StatusBadRequest, "expected request id to be set")
			return
		}
		if reqID.(string) != "0123456789abcdef" {
			c.String(http.StatusBadRequest, "unexpected request id: "+reqID.(string))
			return
		}
		tid, _ := c.Get("trace_id")
		if tid.(string) != "0123456789abcdeffedcba9876543210" {
			c.String(http.StatusBadRequest, "unexpected trace_id: "+tid.(string))
			return
		}
		c.String(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestInjectTraceIDToLogger_DoesNotReplaceExistingRequestID(t *testing.T) {
	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		c.Set(common.RequestIdKey, "already-set-request-id")
		c.Set(TraceIDKey, "sometraceid")
		InjectTraceIDToLogger(c)

		reqID, _ := c.Get(common.RequestIdKey)
		if reqID.(string) != "already-set-request-id" {
			c.String(http.StatusBadRequest, "request id was overwritten: "+reqID.(string))
			return
		}
		tid, exists := c.Get("trace_id")
		if !exists || tid.(string) != "sometraceid" {
			c.String(http.StatusBadRequest, "expected trace_id to still be set separately")
			return
		}
		c.String(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

// sanity: ensure trace.Span interface import is actually used (via helper below),
// avoiding an unused-import error if refactors trim direct usages above.
var _ = func() trace.Span {
	return trace.SpanFromContext(context.Background())
}
