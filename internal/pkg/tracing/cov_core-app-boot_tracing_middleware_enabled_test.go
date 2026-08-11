package tracing

// cov_core-app-boot_tracing_middleware_enabled_test.go — middleware.go's
// enabled=true branches (the existing tracing_test.go only exercises the
// disabled short-circuits). Wires a recorder-backed tracer via the same
// core_app_boot_withRecorderTracer helper as the sibling _internals_test.go
// file, then drives real gin requests through Middleware/RelaySpan/
// SetRelayAttributes/StartChannelSelectSpan/StartUpstreamSpan/StartAuthSpan
// and asserts on the recorded spans' names/attributes/status — not just
// "didn't panic".

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/codes"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestCoreAppBootTracing_Middleware_EnabledSuccessAndError(t *testing.T) {
	t.Run("200_ok_sets_trace_header_and_span", func(t *testing.T) {
		recorder := core_app_boot_withRecorderTracer(t)

		router := gin.New()
		router.Use(Middleware())
		var capturedTraceIDFromCtx string
		router.GET("/orders/:id", func(c *gin.Context) {
			capturedTraceIDFromCtx = GetTraceIDFromContext(c)
			c.String(http.StatusOK, "ok")
		})

		req := httptest.NewRequest(http.MethodGet, "/orders/42", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		headerTraceID := w.Header().Get(TraceIDHeader)
		if headerTraceID == "" {
			t.Fatal("expected X-Trace-Id response header when tracing is enabled")
		}
		if capturedTraceIDFromCtx != headerTraceID {
			t.Errorf("expected GetTraceIDFromContext inside the handler (%q) to match the response header (%q)", capturedTraceIDFromCtx, headerTraceID)
		}

		span := core_app_boot_findSpan(recorder, "GET /orders/:id")
		if span == nil {
			t.Fatal("expected middleware to record a span named 'GET /orders/:id'")
		}
		var sawStatusCode, sawMethod bool
		for _, a := range span.Attributes() {
			if string(a.Key) == "http.status_code" && a.Value.AsInt64() == 200 {
				sawStatusCode = true
			}
			if string(a.Key) == "http.method" && a.Value.AsString() == "GET" {
				sawMethod = true
			}
		}
		if !sawStatusCode {
			t.Error("expected http.status_code=200 attribute on the span")
		}
		if !sawMethod {
			t.Error("expected http.method=GET attribute on the span")
		}
		if span.Status().Code == codes.Error {
			t.Error("expected non-error span status for a 200 response")
		}
	})

	t.Run("500_with_gin_error_marks_span_error", func(t *testing.T) {
		recorder := core_app_boot_withRecorderTracer(t)

		router := gin.New()
		router.Use(Middleware())
		router.GET("/boom", func(c *gin.Context) {
			_ = c.Error(errors.New("downstream failure"))
			c.String(http.StatusInternalServerError, "boom")
		})

		req := httptest.NewRequest(http.MethodGet, "/boom", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", w.Code)
		}

		span := core_app_boot_findSpan(recorder, "GET /boom")
		if span == nil {
			t.Fatal("expected middleware to record a span named 'GET /boom'")
		}
		if span.Status().Code != codes.Error {
			t.Errorf("expected span status Error for a 500 response, got %v", span.Status().Code)
		}
		if len(span.Events()) == 0 {
			t.Error("expected c.Errors to be recorded as an event on the span")
		}
	})
}

func TestCoreAppBootTracing_RelaySpan_Enabled(t *testing.T) {
	recorder := core_app_boot_withRecorderTracer(t)

	router := gin.New()
	router.GET("/relay", func(c *gin.Context) {
		span, end := RelaySpan(c, "chat_completion")
		if span == nil {
			t.Error("expected non-nil span when tracing is enabled")
		}
		end()
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/relay", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if core_app_boot_findSpan(recorder, "relay.chat_completion") == nil {
		t.Fatal("expected a recorded span named 'relay.chat_completion'")
	}
}

func TestCoreAppBootTracing_SetRelayAttributes_Enabled(t *testing.T) {
	recorder := core_app_boot_withRecorderTracer(t)

	router := gin.New()
	router.GET("/relay-attrs", func(c *gin.Context) {
		ctx, span := StartSpan(c.Request.Context(), "parent-relay-span")
		c.Request = c.Request.WithContext(ctx)

		SetRelayAttributes(c, "anthropic", "claude-3-opus", 7)

		span.End()
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/relay-attrs", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	span := core_app_boot_findSpan(recorder, "parent-relay-span")
	if span == nil {
		t.Fatal("expected parent span to be recorded")
	}
	var sawProvider, sawModel, sawChannel bool
	for _, a := range span.Attributes() {
		switch string(a.Key) {
		case "relay.provider":
			sawProvider = a.Value.AsString() == "anthropic"
		case "relay.model":
			sawModel = a.Value.AsString() == "claude-3-opus"
		case "relay.channel_id":
			sawChannel = a.Value.AsInt64() == 7
		}
	}
	if !sawProvider || !sawModel || !sawChannel {
		t.Errorf("expected relay.provider/relay.model/relay.channel_id attributes on the parent span, got %v", span.Attributes())
	}
}

func TestCoreAppBootTracing_StartChannelSelectSpan_Enabled(t *testing.T) {
	t.Run("success_records_selected_id", func(t *testing.T) {
		recorder := core_app_boot_withRecorderTracer(t)
		router := gin.New()
		router.GET("/select", func(c *gin.Context) {
			end := StartChannelSelectSpan(c)
			end(99, nil)
			c.String(http.StatusOK, "ok")
		})
		req := httptest.NewRequest(http.MethodGet, "/select", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		span := core_app_boot_findSpan(recorder, "relay.channel_select")
		if span == nil {
			t.Fatal("expected relay.channel_select span")
		}
		var sawID bool
		for _, a := range span.Attributes() {
			if string(a.Key) == "channel.selected_id" && a.Value.AsInt64() == 99 {
				sawID = true
			}
		}
		if !sawID {
			t.Error("expected channel.selected_id=99 attribute")
		}
		if span.Status().Code == codes.Error {
			t.Error("expected non-error status on success")
		}
	})

	t.Run("error_marks_span_error", func(t *testing.T) {
		recorder := core_app_boot_withRecorderTracer(t)
		router := gin.New()
		router.GET("/select-fail", func(c *gin.Context) {
			end := StartChannelSelectSpan(c)
			end(0, errors.New("no channel available"))
			c.String(http.StatusOK, "ok")
		})
		req := httptest.NewRequest(http.MethodGet, "/select-fail", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		span := core_app_boot_findSpan(recorder, "relay.channel_select")
		if span == nil {
			t.Fatal("expected relay.channel_select span")
		}
		if span.Status().Code != codes.Error {
			t.Errorf("expected error status, got %v", span.Status().Code)
		}
		if len(span.Events()) == 0 {
			t.Error("expected RecordError event on failure")
		}
	})
}

func TestCoreAppBootTracing_StartUpstreamSpan_Enabled(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		recorder := core_app_boot_withRecorderTracer(t)
		router := gin.New()
		router.GET("/upstream", func(c *gin.Context) {
			end := StartUpstreamSpan(c, "openai", "https://api.openai.com/v1/chat/completions")
			end(200, nil)
			c.String(http.StatusOK, "ok")
		})
		req := httptest.NewRequest(http.MethodGet, "/upstream", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		span := core_app_boot_findSpan(recorder, "relay.upstream.openai")
		if span == nil {
			t.Fatal("expected relay.upstream.openai span")
		}
		var sawProvider, sawStatus bool
		for _, a := range span.Attributes() {
			if string(a.Key) == "upstream.provider" && a.Value.AsString() == "openai" {
				sawProvider = true
			}
			if string(a.Key) == "upstream.status_code" && a.Value.AsInt64() == 200 {
				sawStatus = true
			}
		}
		if !sawProvider || !sawStatus {
			t.Errorf("expected upstream.provider=openai and upstream.status_code=200, got %v", span.Attributes())
		}
		if span.Status().Code == codes.Error {
			t.Error("expected non-error status on success")
		}
	})

	t.Run("upstream_failure_marks_error", func(t *testing.T) {
		recorder := core_app_boot_withRecorderTracer(t)
		router := gin.New()
		router.GET("/upstream-fail", func(c *gin.Context) {
			end := StartUpstreamSpan(c, "anthropic", "https://api.anthropic.com")
			end(503, errors.New("upstream unavailable"))
			c.String(http.StatusOK, "ok")
		})
		req := httptest.NewRequest(http.MethodGet, "/upstream-fail", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		span := core_app_boot_findSpan(recorder, "relay.upstream.anthropic")
		if span == nil {
			t.Fatal("expected relay.upstream.anthropic span")
		}
		if span.Status().Code != codes.Error {
			t.Errorf("expected error status for a failed upstream call, got %v", span.Status().Code)
		}
	})
}

func TestCoreAppBootTracing_StartAuthSpan_Enabled(t *testing.T) {
	t.Run("success_records_user_id", func(t *testing.T) {
		recorder := core_app_boot_withRecorderTracer(t)
		router := gin.New()
		router.GET("/auth", func(c *gin.Context) {
			end := StartAuthSpan(c)
			end(1234, nil)
			c.String(http.StatusOK, "ok")
		})
		req := httptest.NewRequest(http.MethodGet, "/auth", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		span := core_app_boot_findSpan(recorder, "auth.validate")
		if span == nil {
			t.Fatal("expected auth.validate span")
		}
		var sawUser bool
		for _, a := range span.Attributes() {
			if string(a.Key) == "auth.user_id" && a.Value.AsInt64() == 1234 {
				sawUser = true
			}
		}
		if !sawUser {
			t.Error("expected auth.user_id=1234 attribute")
		}
	})

	t.Run("failure_marks_error", func(t *testing.T) {
		recorder := core_app_boot_withRecorderTracer(t)
		router := gin.New()
		router.GET("/auth-fail", func(c *gin.Context) {
			end := StartAuthSpan(c)
			end(0, errors.New("invalid token"))
			c.String(http.StatusOK, "ok")
		})
		req := httptest.NewRequest(http.MethodGet, "/auth-fail", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		span := core_app_boot_findSpan(recorder, "auth.validate")
		if span == nil {
			t.Fatal("expected auth.validate span")
		}
		if span.Status().Code != codes.Error {
			t.Errorf("expected error status, got %v", span.Status().Code)
		}
	})
}

// TestCoreAppBootTracing_InjectTraceIDToLogger covers all three branches:
// no trace ID (no-op), trace ID with no pre-existing request ID (sets a
// truncated request ID + full trace ID), and trace ID with a pre-existing
// request ID (must NOT clobber it, only sets trace_id).
func TestCoreAppBootTracing_InjectTraceIDToLogger(t *testing.T) {
	const fullTraceID = "0123456789abcdef0123456789abcdef" // 32 hex chars, like a real OTel trace id (only first 32 used)
	traceID32 := fullTraceID[:32]

	t.Run("no_trace_id_is_noop", func(t *testing.T) {
		router := gin.New()
		router.GET("/x", func(c *gin.Context) {
			InjectTraceIDToLogger(c)
			if c.GetString("trace_id") != "" {
				t.Error("expected trace_id to remain unset")
			}
			if c.GetString(common.RequestIdKey) != "" {
				t.Error("expected request id key to remain unset")
			}
			c.String(http.StatusOK, "ok")
		})
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
	})

	t.Run("sets_truncated_request_id_when_absent", func(t *testing.T) {
		router := gin.New()
		router.GET("/x", func(c *gin.Context) {
			c.Set(TraceIDKey, traceID32)
			InjectTraceIDToLogger(c)
			if got := c.GetString("trace_id"); got != traceID32 {
				t.Errorf("expected trace_id=%q, got %q", traceID32, got)
			}
			if got := c.GetString(common.RequestIdKey); got != traceID32[:16] {
				t.Errorf("expected request id to be first 16 chars %q, got %q", traceID32[:16], got)
			}
			c.String(http.StatusOK, "ok")
		})
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
	})

	t.Run("does_not_clobber_existing_request_id", func(t *testing.T) {
		router := gin.New()
		router.GET("/x", func(c *gin.Context) {
			c.Set(common.RequestIdKey, "req-existing-123")
			c.Set(TraceIDKey, traceID32)
			InjectTraceIDToLogger(c)
			if got := c.GetString(common.RequestIdKey); got != "req-existing-123" {
				t.Errorf("expected existing request id preserved, got %q", got)
			}
			if got := c.GetString("trace_id"); got != traceID32 {
				t.Errorf("expected trace_id=%q, got %q", traceID32, got)
			}
			c.String(http.StatusOK, "ok")
		})
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
	})
}
