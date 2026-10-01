package claude

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// A native Anthropic upstream that stops before message_delta (claudeInfo.Done
// false) leaves a partial answer. Since 2026-09-02 the caller is told it is
// partial: before, the OpenAI wire got a usage frame + [DONE] and the Claude
// wire a bare EOF, both of which read as a normal end. Since 2026-09-30 the
// failure is also returned from handleStreamFinalResponse (and so from
// ClaudeStreamHandler: nothing billed, breaker failure); the exported void
// HandleStreamFinalResponse, still used by the bedrock stream, keeps the old
// side-effect-only shape.
func newIncompleteClaudeCtx(t *testing.T, format types.RelayFormat, clientGone bool) (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo, *ClaudeResponseInfo) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	if clientGone {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c.Request = c.Request.WithContext(ctx)
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta:        &relaycommon.ChannelMeta{UpstreamModelName: "claude-x"},
		RelayFormat:        format,
		ShouldIncludeUsage: true,
		StreamEndReason:    relaycommon.StreamEndUpstreamClosed,
	}
	claudeInfo := newClaudeInfo()
	claudeInfo.Usage.PromptTokens = 5
	claudeInfo.Done = false
	claudeInfo.ResponseText.WriteString("partial answer")
	return c, w, info, claudeInfo
}

func TestHandleStreamFinalResponse_Incomplete_ErrorFrameOnEveryWire(t *testing.T) {
	t.Run("claude wire: event error, no message_stop", func(t *testing.T) {
		c, w, info, claudeInfo := newIncompleteClaudeCtx(t, types.RelayFormatClaude, false)
		HandleStreamFinalResponse(c, info, claudeInfo, RequestModeMessage)
		body := w.Body.String()
		if !strings.Contains(body, "event: error\ndata: {\"type\":\"error\",\"error\":{") || !strings.Contains(body, "upstream stream ended before completion") {
			t.Errorf("claude-wire caller must get an `event: error` frame:\n%s", body)
		}
		if strings.Contains(body, "message_stop") || strings.Contains(body, "[DONE]") {
			t.Errorf("no invented normal end:\n%s", body)
		}
		if claudeInfo.Usage.CompletionTokens <= 0 {
			t.Errorf("partial text is still billed (pre-existing rule): %+v", claudeInfo.Usage)
		}
	})

	t.Run("openai wire: error frame instead of usage frame", func(t *testing.T) {
		c, w, info, claudeInfo := newIncompleteClaudeCtx(t, types.RelayFormatOpenAI, false)
		HandleStreamFinalResponse(c, info, claudeInfo, RequestModeMessage)
		body := w.Body.String()
		if !strings.Contains(body, `data: {"error":{"message":"upstream stream ended before completion"`) {
			t.Errorf("openai-wire caller must get an error frame:\n%s", body)
		}
		if strings.Contains(body, `"usage":{"prompt_tokens"`) {
			t.Errorf("no usage frame dressed up as a normal end:\n%s", body)
		}
		if !strings.HasSuffix(strings.TrimSpace(body), "data: [DONE]") {
			t.Errorf("openai wire still terminates with [DONE]:\n%s", body)
		}
	})

	t.Run("idle timeout reads as 504", func(t *testing.T) {
		c, w, info, claudeInfo := newIncompleteClaudeCtx(t, types.RelayFormatOpenAI, false)
		info.StreamEndReason = relaycommon.StreamEndTimeout
		HandleStreamFinalResponse(c, info, claudeInfo, RequestModeMessage)
		if !strings.Contains(w.Body.String(), "idle timeout before completion") {
			t.Errorf("timeout reason lost:\n%s", w.Body.String())
		}
	})

	t.Run("caller hung up: nothing written", func(t *testing.T) {
		c, w, info, claudeInfo := newIncompleteClaudeCtx(t, types.RelayFormatClaude, true)
		HandleStreamFinalResponse(c, info, claudeInfo, RequestModeMessage)
		if w.Body.Len() != 0 {
			t.Errorf("no frame for a caller that hung up:\n%s", w.Body.String())
		}
	})
}

// handleStreamFinalResponse returns what HandleStreamFinalResponse only
// renders: the surfaced incomplete-stream error when the caller is still
// listening, nil for a complete answer, completion mode and a hung-up caller.
func TestHandleStreamFinalResponse_ReturnsSurfacedErrorOnlyWhenCallerListening(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {
		c, w, info, claudeInfo := newIncompleteClaudeCtx(t, format, false)
		apiErr := handleStreamFinalResponse(c, info, claudeInfo, RequestModeMessage)
		if apiErr == nil || !helper.IsIncompleteStreamSurfaced(apiErr) {
			t.Fatalf("%s: want the surfaced incomplete-stream error, got %v", format, apiErr)
		}
		if w.Body.Len() == 0 {
			t.Errorf("%s: the in-band frame must have been written before returning", format)
		}

		c, w, info, claudeInfo = newIncompleteClaudeCtx(t, format, true)
		if apiErr := handleStreamFinalResponse(c, info, claudeInfo, RequestModeMessage); apiErr != nil || w.Body.Len() != 0 {
			t.Errorf("%s: hung-up caller: want nil error and no frame, got %v / %q", format, apiErr, w.Body.String())
		}

		c, _, info, claudeInfo = newIncompleteClaudeCtx(t, format, false)
		claudeInfo.Done = true
		if apiErr := handleStreamFinalResponse(c, info, claudeInfo, RequestModeMessage); apiErr != nil {
			t.Errorf("%s: complete answer must return nil, got %v", format, apiErr)
		}
	}
	c, _, info, claudeInfo := newIncompleteClaudeCtx(t, types.RelayFormatOpenAI, false)
	if apiErr := handleStreamFinalResponse(c, info, claudeInfo, RequestModeCompletion); apiErr != nil {
		t.Errorf("completion mode has no message_delta and never reports incomplete, got %v", apiErr)
	}
}

// message_delta seen: the answer is complete and the OpenAI-wire caller keeps
// its usage frame + [DONE]; completion mode has no message_delta at all and
// keeps its behaviour too.
func TestHandleStreamFinalResponse_Complete_KeepsUsageFrame(t *testing.T) {
	c, w, info, claudeInfo := newIncompleteClaudeCtx(t, types.RelayFormatOpenAI, false)
	claudeInfo.Done = true
	claudeInfo.Usage.CompletionTokens = 7
	HandleStreamFinalResponse(c, info, claudeInfo, RequestModeMessage)
	body := w.Body.String()
	if !strings.Contains(body, `"completion_tokens":7`) || !strings.HasSuffix(strings.TrimSpace(body), "data: [DONE]") {
		t.Errorf("complete stream must keep its usage frame + [DONE]:\n%s", body)
	}
	if strings.Contains(body, "upstream_stream_incomplete") {
		t.Errorf("complete stream must not be reported as incomplete:\n%s", body)
	}

	c, w, info, claudeInfo = newIncompleteClaudeCtx(t, types.RelayFormatOpenAI, false)
	HandleStreamFinalResponse(c, info, claudeInfo, RequestModeCompletion)
	if strings.Contains(w.Body.String(), "upstream_stream_incomplete") {
		t.Errorf("completion mode has no message_delta; must not be reported as incomplete:\n%s", w.Body.String())
	}
}
