package openai

// Business-acceptance tests filling the remaining branch gaps in helper.go's
// non-OpenAI wire-format paths: the Claude/Gemini "happy path" of
// HandleStreamFormat (handleClaudeFormat/handleGeminiFormat), the
// content-less "skip, don't emit a frame" branch that keeps SSE streams from
// sending empty Gemini candidates, HandleFinalResponse's Gemini branch, and
// sendResponsesStreamData's empty-data guard. A regression here means a
// Claude- or Gemini-format client either gets malformed frames or silently
// drops the last chunk of a response.

import (
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func TestHandleClaudeFormat_ValidChunk_EmitsMessageStart(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		// HandleStreamFormat increments SendResponseCount before dispatch, so
		// starting at 0 makes this the first chunk -> message_start frame.
		SendResponseCount: 0,
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{},
	}
	data := `{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"hi"}}]}`
	if err := HandleStreamFormat(w.ctx, info, data, false, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := w.rec.Body.String()
	if !strings.Contains(body, "message_start") {
		t.Errorf("body = %q, want a message_start event (proves the Claude success branch ran, not just error-propagation)", body)
	}
	if !strings.Contains(body, `"hi"`) {
		t.Errorf("body = %q, want the delta content to appear in the emitted frame", body)
	}
}

func TestHandleClaudeFormat_CapturesUsageOntoClaudeConvertInfo(t *testing.T) {
	w := newRecorderCtx(t)
	// ChannelMeta is always initialised in production (RelayInfo.InitChannelMeta)
	// and the chunk's usage is now wire-stamped through applyUsagePostProcessing,
	// which reads the channel type; a bare RelayInfo is not a production shape.
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude, SendResponseCount: 2, ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{},
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
	data := `{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`
	if err := HandleStreamFormat(w.ctx, info, data, false, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Usage == nil {
		t.Fatal("expected ClaudeConvertInfo.Usage to be populated from the chunk's usage field")
	}
	if info.Usage.TotalTokens != 7 {
		t.Errorf("ClaudeConvertInfo.Usage.TotalTokens = %d, want 7", info.Usage.TotalTokens)
	}
}

func TestHandleGeminiFormat_ContentChunk_RendersSSEFrame(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatGemini}
	data := `{"id":"c1","choices":[{"index":0,"delta":{"content":"hello"}}]}`
	if err := HandleStreamFormat(w.ctx, info, data, false, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := w.rec.Body.String()
	if !strings.HasPrefix(strings.TrimSpace(body), "data: ") {
		t.Fatalf("body = %q, want an SSE data: frame", body)
	}
	if !strings.Contains(body, "hello") {
		t.Errorf("body = %q, want the delta content translated into the Gemini candidate", body)
	}
}

// A chunk with neither content nor a finish reason (the leading empty chunk
// some upstreams send) must be silently swallowed: no bytes written, no
// error. Sending an empty Gemini candidate for it would corrupt the client's
// SSE stream.
func TestHandleGeminiFormat_EmptyLeadingChunk_WritesNothing(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatGemini}
	data := `{"id":"c1","choices":[{"index":0,"delta":{}}]}`
	if err := HandleStreamFormat(w.ctx, info, data, false, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body := w.rec.Body.String(); body != "" {
		t.Errorf("body = %q, want empty (content-less, finish-reason-less chunk must be dropped, not forwarded)", body)
	}
}

func TestHandleFinalResponse_GeminiFormat_TranslatesFinishReason(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatGemini}
	usage := &dto.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}
	lastData := `{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	HandleFinalResponse(w.ctx, info, lastData, "c1", 1700000000, "gpt-4o", "fp", usage, false)
	body := w.rec.Body.String()
	if !strings.Contains(body, `"STOP"`) {
		t.Errorf("body = %q, want the OpenAI finish_reason=stop translated to Gemini's STOP", body)
	}
}

// A terminal chunk with no content, no finish reason and a ZEROED usage is an
// abnormal end (the handler zeroes usage when no [DONE] was seen, and nothing
// is billed): no frame is invented for it. With a real usage the same chunk
// becomes the terminal usageMetadata frame — see
// TestOaiStreamHandler_GeminiWire_SingleStopFrameCarriesBilledUsage.
func TestHandleFinalResponse_GeminiFormat_ContentlessChunkWritesNothing(t *testing.T) {
	w := newRecorderCtx(t)
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatGemini}
	usage := &dto.Usage{}
	lastData := `{"id":"c1","choices":[{"index":0,"delta":{}}]}`
	HandleFinalResponse(w.ctx, info, lastData, "c1", 0, "gpt-4o", "fp", usage, false)
	if body := w.rec.Body.String(); body != "" {
		t.Errorf("body = %q, want empty (no content, no finish_reason -> must not emit a Gemini frame)", body)
	}
}

func TestSendResponsesStreamData_EmptyData_NoOp(t *testing.T) {
	w := newRecorderCtx(t)
	sendResponsesStreamData(w.ctx, dto.ResponsesStreamResponse{Type: "response.output_text.delta"}, "")
	if body := w.rec.Body.String(); body != "" {
		t.Errorf("body = %q, want empty: an empty data payload must be a no-op, not an empty SSE frame", body)
	}
}

func TestSendResponsesStreamData_NonEmptyData_EmitsChunk(t *testing.T) {
	w := newRecorderCtx(t)
	sendResponsesStreamData(w.ctx, dto.ResponsesStreamResponse{Type: "response.output_text.delta"}, `{"delta":"hi"}`)
	body := w.rec.Body.String()
	if !strings.Contains(body, "response.output_text.delta") {
		t.Errorf("body = %q, want the event type echoed into the SSE 'event:' line", body)
	}
	if !strings.Contains(body, `"delta":"hi"`) {
		t.Errorf("body = %q, want the raw data payload forwarded verbatim", body)
	}
}
