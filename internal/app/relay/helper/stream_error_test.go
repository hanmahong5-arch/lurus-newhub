package helper

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// StreamError must produce the one in-band shape each official SDK raises on.
// Verified against the SDK sources on 2026-09-02: openai-python raises on any
// data frame whose JSON has an "error" key; anthropic-sdk-python dispatches on
// the SSE event name and silently ignores frames without one; google-genai
// raises on a frame starting with {"error": and json-decodes every line (so a
// [DONE] line becomes a decode error instead of the real one).
func TestStreamError_WireNativeShapes(t *testing.T) {
	apiErr := types.NewErrorWithStatusCode(errors.New("upstream went away"), types.ErrorCodeBadResponse, http.StatusBadGateway)

	t.Run("openai: error key then [DONE]", func(t *testing.T) {
		c, w := newStreamCtx()
		StreamError(c, types.RelayFormatOpenAI, apiErr)
		body := w.Body.String()
		if !strings.Contains(body, `data: {"error":{`) || !strings.Contains(body, `"message":"upstream went away"`) {
			t.Errorf("openai wire error frame missing: %q", body)
		}
		if !strings.HasSuffix(strings.TrimSpace(body), "data: [DONE]") {
			t.Errorf("openai wire must still terminate with [DONE]: %q", body)
		}
	})

	t.Run("claude: event line is error, no [DONE]", func(t *testing.T) {
		c, w := newStreamCtx()
		StreamError(c, types.RelayFormatClaude, apiErr)
		body := w.Body.String()
		if !strings.Contains(body, "event: error\n") {
			t.Errorf("anthropic SDK dispatches on the event line; missing: %q", body)
		}
		if !strings.Contains(body, `data: {"type":"error","error":{`) || !strings.Contains(body, `"message":"upstream went away"`) {
			t.Errorf("claude wire error payload wrong: %q", body)
		}
		if strings.Contains(body, "[DONE]") {
			t.Errorf("[DONE] is not an Anthropic-wire token: %q", body)
		}
	})

	t.Run("gemini: {\"error\":{code,message,status}} first, no [DONE]", func(t *testing.T) {
		c, w := newStreamCtx()
		StreamError(c, types.RelayFormatGemini, apiErr)
		body := w.Body.String()
		if !strings.HasPrefix(body, `data: {"error":{"code":502,"message":"upstream went away","status":"UNAVAILABLE"}}`) {
			t.Errorf("google-genai checks the {\"error\": prefix; got %q", body)
		}
		if strings.Contains(body, "[DONE]") {
			t.Errorf("a [DONE] line makes google-genai raise a decode error instead of the real one: %q", body)
		}
	})

	t.Run("nil guards", func(t *testing.T) {
		c, w := newStreamCtx()
		StreamError(nil, types.RelayFormatOpenAI, apiErr)
		StreamError(c, types.RelayFormatOpenAI, nil)
		if w.Body.Len() != 0 {
			t.Errorf("nil inputs must write nothing: %q", w.Body.String())
		}
	})
}

func TestGeminiStatus(t *testing.T) {
	for code, want := range map[int]string{
		400: "INVALID_ARGUMENT", 401: "UNAUTHENTICATED", 403: "PERMISSION_DENIED", 404: "NOT_FOUND",
		429: "RESOURCE_EXHAUSTED", 500: "INTERNAL", 502: "UNAVAILABLE", 503: "UNAVAILABLE",
		504: "DEADLINE_EXCEEDED", 418: "UNKNOWN",
	} {
		if got := geminiStatus(code); got != want {
			t.Errorf("geminiStatus(%d) = %s, want %s", code, got, want)
		}
	}
}

func TestClientListening(t *testing.T) {
	c, _ := newStreamCtx()
	if !ClientListening(c, &relaycommon.RelayInfo{}) {
		t.Error("live request, no end reason: must be listening")
	}
	if ClientListening(c, &relaycommon.RelayInfo{StreamEndReason: relaycommon.StreamEndClientGone}) {
		t.Error("scanner saw the caller hang up: must not be listening")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	if ClientListening(c, &relaycommon.RelayInfo{}) {
		t.Error("cancelled request context: must not be listening")
	}
	if ClientListening(nil, nil) {
		t.Error("nil context: must not be listening")
	}
}

func TestIncompleteStreamError(t *testing.T) {
	closed := IncompleteStreamError(&relaycommon.RelayInfo{StreamEndReason: relaycommon.StreamEndUpstreamClosed})
	if closed.StatusCode != http.StatusBadGateway || closed.GetErrorCode() != types.ErrorCodeUpstreamStreamIncomplete {
		t.Errorf("upstream closed: got %d/%s", closed.StatusCode, closed.GetErrorCode())
	}
	timeout := IncompleteStreamError(&relaycommon.RelayInfo{StreamEndReason: relaycommon.StreamEndTimeout})
	if timeout.StatusCode != http.StatusGatewayTimeout || !strings.Contains(timeout.Error(), "idle timeout") {
		t.Errorf("idle timeout: got %d %q", timeout.StatusCode, timeout.Error())
	}
	// Frames already left the process: a retry would append a second answer.
	if !types.IsSkipRetryError(closed) || !types.IsSkipRetryError(timeout) {
		t.Error("incomplete-stream errors must never be retried")
	}
	if IncompleteStreamError(nil).StatusCode != http.StatusBadGateway {
		t.Error("nil info must default to 502")
	}
}
