package claude

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func newClaudeInfo() *ClaudeResponseInfo {
	return &ClaudeResponseInfo{
		Usage: &dto.Usage{},
	}
}

// ---------------------------------------------------------------------------
// FormatClaudeResponseInfo
// ---------------------------------------------------------------------------

func TestFormatClaudeResponseInfo_CompletionModeAccumulatesText(t *testing.T) {
	info := newClaudeInfo()
	ok := FormatClaudeResponseInfo(RequestModeCompletion, &dto.ClaudeResponse{Completion: "Hello"}, nil, info)
	if !ok {
		t.Fatal("expected true")
	}
	ok = FormatClaudeResponseInfo(RequestModeCompletion, &dto.ClaudeResponse{Completion: " world"}, nil, info)
	if !ok {
		t.Fatal("expected true")
	}
	if info.ResponseText.String() != "Hello world" {
		t.Errorf("ResponseText = %q, want %q", info.ResponseText.String(), "Hello world")
	}
}

func TestFormatClaudeResponseInfo_MessageStart_ExtractsUsageAndMeta(t *testing.T) {
	info := newClaudeInfo()
	claudeResp := &dto.ClaudeResponse{
		Type: "message_start",
		Message: &dto.ClaudeMediaMessage{
			Id:    "msg_abc",
			Model: "claude-3-opus-20240229",
			Usage: &dto.ClaudeUsage{
				InputTokens:              10,
				OutputTokens:             2,
				CacheReadInputTokens:     3,
				CacheCreationInputTokens: 4,
			},
		},
	}
	oaiResp := &dto.ChatCompletionsStreamResponse{}
	ok := FormatClaudeResponseInfo(RequestModeMessage, claudeResp, oaiResp, info)
	if !ok {
		t.Fatal("expected true")
	}
	if info.ResponseId != "msg_abc" {
		t.Errorf("ResponseId = %q, want %q", info.ResponseId, "msg_abc")
	}
	if info.Model != "claude-3-opus-20240229" {
		t.Errorf("Model = %q, want %q", info.Model, "claude-3-opus-20240229")
	}
	if info.Usage.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want 10", info.Usage.PromptTokens)
	}
	if info.Usage.CompletionTokens != 2 {
		t.Errorf("CompletionTokens = %d, want 2", info.Usage.CompletionTokens)
	}
	if info.Usage.PromptTokensDetails.CachedTokens != 3 {
		t.Errorf("CachedTokens = %d, want 3", info.Usage.PromptTokensDetails.CachedTokens)
	}
	// oaiResponse should be populated from claudeInfo meta
	if oaiResp.Id != "msg_abc" || oaiResp.Model != "claude-3-opus-20240229" {
		t.Errorf("oaiResp = %+v, want Id=msg_abc Model=claude-3-opus-20240229", oaiResp)
	}
}

func TestFormatClaudeResponseInfo_ContentBlockDelta_TextAndThinking(t *testing.T) {
	info := newClaudeInfo()
	textResp := &dto.ClaudeResponse{
		Type:  "content_block_delta",
		Delta: &dto.ClaudeMediaMessage{Text: common.GetPointer("chunk1")},
	}
	if ok := FormatClaudeResponseInfo(RequestModeMessage, textResp, nil, info); !ok {
		t.Fatal("expected true")
	}
	thinkResp := &dto.ClaudeResponse{
		Type:  "content_block_delta",
		Delta: &dto.ClaudeMediaMessage{Thinking: common.GetPointer(" reasoning")},
	}
	if ok := FormatClaudeResponseInfo(RequestModeMessage, thinkResp, nil, info); !ok {
		t.Fatal("expected true")
	}
	if info.ResponseText.String() != "chunk1 reasoning" {
		t.Errorf("ResponseText = %q, want %q", info.ResponseText.String(), "chunk1 reasoning")
	}
}

func TestFormatClaudeResponseInfo_MessageDelta_UsageAndDone(t *testing.T) {
	info := newClaudeInfo()
	info.Usage.PromptTokens = 99 // pre-existing from message_start
	claudeResp := &dto.ClaudeResponse{
		Type:  "message_delta",
		Usage: &dto.ClaudeUsage{InputTokens: 0, OutputTokens: 7}, // InputTokens==0 must NOT overwrite PromptTokens
	}
	if ok := FormatClaudeResponseInfo(RequestModeMessage, claudeResp, nil, info); !ok {
		t.Fatal("expected true")
	}
	if info.Usage.PromptTokens != 99 {
		t.Errorf("PromptTokens = %d, want unchanged 99 (InputTokens=0 must not overwrite)", info.Usage.PromptTokens)
	}
	if info.Usage.CompletionTokens != 7 {
		t.Errorf("CompletionTokens = %d, want 7", info.Usage.CompletionTokens)
	}
	if info.Usage.TotalTokens != 106 {
		t.Errorf("TotalTokens = %d, want 106", info.Usage.TotalTokens)
	}
	if !info.Done {
		t.Error("Done should be true after message_delta")
	}
}

func TestFormatClaudeResponseInfo_MessageDelta_InputTokensOverwritesWhenPositive(t *testing.T) {
	info := newClaudeInfo()
	info.Usage.PromptTokens = 99
	claudeResp := &dto.ClaudeResponse{
		Type:  "message_delta",
		Usage: &dto.ClaudeUsage{InputTokens: 55, OutputTokens: 1},
	}
	FormatClaudeResponseInfo(RequestModeMessage, claudeResp, nil, info)
	if info.Usage.PromptTokens != 55 {
		t.Errorf("PromptTokens = %d, want overwritten to 55 (InputTokens>0)", info.Usage.PromptTokens)
	}
}

func TestFormatClaudeResponseInfo_ContentBlockStart_NoOpTrue(t *testing.T) {
	info := newClaudeInfo()
	ok := FormatClaudeResponseInfo(RequestModeMessage, &dto.ClaudeResponse{Type: "content_block_start"}, nil, info)
	if !ok {
		t.Fatal("expected true for content_block_start (no-op branch)")
	}
	if info.ResponseText.Len() != 0 {
		t.Errorf("ResponseText should stay empty, got %q", info.ResponseText.String())
	}
}

func TestFormatClaudeResponseInfo_UnknownType_ReturnsFalse(t *testing.T) {
	info := newClaudeInfo()
	ok := FormatClaudeResponseInfo(RequestModeMessage, &dto.ClaudeResponse{Type: "message_stop"}, nil, info)
	if ok {
		t.Error("expected false for unrecognized/terminal type message_stop")
	}
}

// FINDING: internal/adapter/provider/claude/relay-claude.go:596-605 (message_start
// branch of FormatClaudeResponseInfo) unconditionally dereferences
// claudeResponse.Message and claudeResponse.Message.Usage:
//
//	claudeInfo.ResponseId = claudeResponse.Message.Id
//	...
//	claudeInfo.Usage.PromptTokens = claudeResponse.Message.Usage.InputTokens
//
// dto.ClaudeResponse.Message is `*ClaudeMediaMessage` and ClaudeMediaMessage.Usage
// is `*ClaudeUsage`, both optional per the `omitempty` json tags. A malformed or
// truncated upstream "message_start" SSE event that omits "message" (or omits
// "usage" within it) makes this a nil-pointer panic instead of a handled parse
// error. Expected: a defensive nil check with a bad_response_body-style error
// (as GetClaudeError already does for the sibling "error" event type). Actual:
// panic. This function has no guard; the panic in this test is recovered
// locally only to prove the defect without crashing the test binary — in
// production (ClaudeStreamHandler's SafeGo wrapper) it silently aborts the
// individual SSE data-handler goroutine, stalling that stream.
func TestFormatClaudeResponseInfo_MessageStart_NilMessage_Panics(t *testing.T) {
	info := newClaudeInfo()
	claudeResp := &dto.ClaudeResponse{Type: "message_start", Message: nil}

	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		FormatClaudeResponseInfo(RequestModeMessage, claudeResp, nil, info)
	}()

	if !panicked {
		t.Fatal("expected a nil-pointer panic for message_start with Message==nil (see FINDING above); if this no longer panics, the nil-guard has been added — please update/remove this regression test")
	}
}

// ---------------------------------------------------------------------------
// HandleClaudeResponseData
// ---------------------------------------------------------------------------

// NOTE: a top-level "usage" object is included here purely to sidestep the
// unconditional nil-deref documented in
// TestHandleClaudeResponseData_CompletionMode_NoUsageField_Panics below; the
// real legacy Anthropic /v1/complete API does not return one.
func TestHandleClaudeResponseData_CompletionMode_ClaudePassthrough(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/complete", nil)

	raw := []byte(`{"completion":" hi there","stop_reason":"stop_sequence","model":"claude-2.1","usage":{"input_tokens":1,"output_tokens":1}}`)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-2.1"},
		RelayFormat: types.RelayFormatClaude,
	}
	claudeInfo := newClaudeInfo()
	httpResp := &http.Response{StatusCode: 200, Header: http.Header{}}

	apiErr := HandleClaudeResponseData(c, info, claudeInfo, httpResp, raw, RequestModeCompletion)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != string(raw) {
		t.Errorf("body = %q, want passthrough of raw upstream bytes %q", w.Body.String(), string(raw))
	}
	// Completion mode computes claudeInfo.Usage via the text estimator (not
	// from the JSON "usage" field), so it should reflect the completion text
	// regardless of what the (here, synthetic) top-level usage object says.
	if claudeInfo.Usage == nil || claudeInfo.Usage.CompletionTokens <= 0 {
		t.Errorf("expected estimated usage tokens > 0 for non-empty completion text, got %+v", claudeInfo.Usage)
	}
}

// FINDING: internal/adapter/provider/claude/relay-claude.go:762 —
//
//	if claudeResponse.Usage.ServerToolUse != nil && claudeResponse.Usage.ServerToolUse.WebSearchRequests > 0 {
//
// runs UNCONDITIONALLY after the requestMode switch, for BOTH RequestModeCompletion
// and RequestModeMessage. For RequestModeCompletion, claudeInfo.Usage is computed
// from claudeResponse.Completion via the text estimator — claudeResponse.Usage
// (the raw upstream *ClaudeUsage pointer) is never populated by that branch. The
// legacy Anthropic /v1/complete API (used for claude-2.x / claude-instant models,
// RequestModeCompletion — see Adaptor.Init) does not return a top-level "usage"
// object at all, so claudeResponse.Usage is nil and this line unconditionally
// nil-derefs on every real non-streaming completion-mode response. Expected: a
// nil check (`claudeResponse.Usage != nil && ...`) matching the optional/
// `omitempty` nature of the field. Actual: guaranteed panic for the mainline
// completion-mode success path. Recovered locally here only to document the
// defect without crashing the suite.
func TestHandleClaudeResponseData_CompletionMode_NoUsageField_Panics(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/complete", nil)

	// This is the real shape of a legacy /v1/complete response: no "usage" key.
	raw := []byte(`{"completion":" hi there","stop_reason":"stop_sequence","model":"claude-2.1"}`)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-2.1"},
		RelayFormat: types.RelayFormatClaude,
	}
	claudeInfo := newClaudeInfo()
	httpResp := &http.Response{StatusCode: 200, Header: http.Header{}}

	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		_ = HandleClaudeResponseData(c, info, claudeInfo, httpResp, raw, RequestModeCompletion)
	}()
	if !panicked {
		t.Fatal("expected a nil-pointer panic for a realistic /v1/complete response with no top-level usage field (see FINDING above); if this no longer panics, the nil-guard has been added — please update/remove this regression test")
	}
}

func TestHandleClaudeResponseData_MessageMode_OpenAIConversion(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	raw := []byte(`{"id":"msg_1","type":"message","content":[{"type":"text","text":"the answer"}],"stop_reason":"end_turn","model":"claude-3-opus-20240229","usage":{"input_tokens":11,"output_tokens":4}}`)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-opus-20240229"},
		RelayFormat: types.RelayFormatOpenAI,
	}
	claudeInfo := newClaudeInfo()
	httpResp := &http.Response{StatusCode: 200, Header: http.Header{}}

	apiErr := HandleClaudeResponseData(c, info, claudeInfo, httpResp, raw, RequestModeMessage)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if claudeInfo.Usage.PromptTokens != 11 || claudeInfo.Usage.CompletionTokens != 4 {
		t.Errorf("Usage = %+v, want PromptTokens=11 CompletionTokens=4", claudeInfo.Usage)
	}
	if claudeInfo.Usage.TotalTokens != 15 {
		t.Errorf("TotalTokens = %d, want 15", claudeInfo.Usage.TotalTokens)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"the answer"`) {
		t.Errorf("converted OpenAI body should contain the response text, got %q", body)
	}
	if !strings.Contains(body, `"chat.completion"`) {
		t.Errorf("converted OpenAI body should be an object=chat.completion payload, got %q", body)
	}
}

func TestHandleClaudeResponseData_ErrorResponse_ReturnsClaudeError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	raw := []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"missing max_tokens"}}`)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-opus-20240229"},
		RelayFormat: types.RelayFormatOpenAI,
	}
	claudeInfo := newClaudeInfo()
	httpResp := &http.Response{StatusCode: 400, Header: http.Header{}}

	apiErr := HandleClaudeResponseData(c, info, claudeInfo, httpResp, raw, RequestModeMessage)
	if apiErr == nil {
		t.Fatal("expected an error for a Claude error-typed response")
	}
	if apiErr.GetErrorCode() != "invalid_request_error" {
		t.Errorf("error code = %q, want %q", apiErr.GetErrorCode(), "invalid_request_error")
	}
	if !strings.Contains(apiErr.Error(), "missing max_tokens") {
		t.Errorf("error message = %q, want to contain upstream message", apiErr.Error())
	}
}

func TestHandleClaudeResponseData_MalformedJSON_ReturnsBadResponseBody(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	claudeInfo := newClaudeInfo()
	httpResp := &http.Response{StatusCode: 200, Header: http.Header{}}

	apiErr := HandleClaudeResponseData(c, info, claudeInfo, httpResp, []byte(`{not-json`), RequestModeMessage)
	if apiErr == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeBadResponseBody {
		t.Errorf("error code = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeBadResponseBody)
	}
}

func TestHandleClaudeResponseData_WebSearchRequestsSetOnContext(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	raw := []byte(`{"id":"msg_ws","type":"message","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","model":"claude-3-opus-20240229","usage":{"input_tokens":1,"output_tokens":1,"server_tool_use":{"web_search_requests":3}}}`)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-opus-20240229"},
		RelayFormat: types.RelayFormatClaude,
	}
	claudeInfo := newClaudeInfo()
	httpResp := &http.Response{StatusCode: 200, Header: http.Header{}}

	apiErr := HandleClaudeResponseData(c, info, claudeInfo, httpResp, raw, RequestModeMessage)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	val, exists := c.Get("claude_web_search_requests")
	if !exists {
		t.Fatal("expected claude_web_search_requests to be set on context")
	}
	if val.(int) != 3 {
		t.Errorf("claude_web_search_requests = %v, want 3", val)
	}
}

// FINDING: internal/adapter/provider/claude/relay-claude.go:740-747
// (HandleClaudeResponseData, non-completion branch) dereferences
// claudeResponse.Usage unconditionally:
//
//	claudeInfo.Usage.PromptTokens = claudeResponse.Usage.InputTokens
//
// dto.ClaudeResponse.Usage is `*ClaudeUsage` (`json:"usage,omitempty"`). The
// preceding GetClaudeError() check only protects the case where the upstream
// response has an explicit "error" field; a response that is neither a valid
// success payload (with "usage") nor an explicit Claude error (e.g. a
// truncated/malformed-but-JSON-valid body, or a non-conformant proxy in
// front of the real Claude API) produces a nil-pointer panic here instead of
// a handled bad_response_body error. Expected: graceful error. Actual: panic
// (recovered locally in this test only to document the defect without
// crashing the suite; in ClaudeHandler's real call path there is no local
// recover, so it propagates up to the global RelayPanicRecover middleware and
// surfaces as an ungraceful 500 for what could have been a clean upstream
// error response).
func TestHandleClaudeResponseData_MessageMode_MissingUsageAndError_Panics(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	raw := []byte(`{"id":"msg_bad","type":"message","content":[{"type":"text","text":"partial"}],"stop_reason":"end_turn","model":"claude-3-opus-20240229"}`)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-opus-20240229"}}
	claudeInfo := newClaudeInfo()
	httpResp := &http.Response{StatusCode: 200, Header: http.Header{}}

	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		_ = HandleClaudeResponseData(c, info, claudeInfo, httpResp, raw, RequestModeMessage)
	}()
	if !panicked {
		t.Fatal("expected a nil-pointer panic when usage is absent and there is no error field (see FINDING above); if this no longer panics, the nil-guard has been added — please update/remove this regression test")
	}
}

// ---------------------------------------------------------------------------
// ClaudeHandler
// ---------------------------------------------------------------------------

func TestClaudeHandler_SuccessEndToEnd(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	body := `{"id":"msg_e2e","type":"message","content":[{"type":"text","text":"complete answer"}],"stop_reason":"end_turn","model":"claude-3-opus-20240229","usage":{"input_tokens":8,"output_tokens":6}}`
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-opus-20240229"},
		RelayFormat: types.RelayFormatClaude,
	}
	usage, apiErr := ClaudeHandler(c, resp, info, RequestModeMessage)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if usage == nil || usage.PromptTokens != 8 || usage.CompletionTokens != 6 {
		t.Errorf("usage = %+v, want PromptTokens=8 CompletionTokens=6", usage)
	}
	if w.Body.String() != body {
		t.Errorf("body = %q, want passthrough %q", w.Body.String(), body)
	}
}

type errReader struct{}

func (errReader) Read(p []byte) (int, error) { return 0, errors.New("simulated read failure") }

func TestClaudeHandler_BodyReadFailure_ReturnsError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       io.NopCloser(errReader{}),
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	_, apiErr := ClaudeHandler(c, resp, info, RequestModeMessage)
	if apiErr == nil {
		t.Fatal("expected error when the response body fails to read")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeBadResponseBody {
		t.Errorf("error code = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeBadResponseBody)
	}
}

func TestClaudeHandler_MalformedUpstreamBody_PropagatesHandlerError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{not-json`)),
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	usage, apiErr := ClaudeHandler(c, resp, info, RequestModeMessage)
	if apiErr == nil {
		t.Fatal("expected the malformed-body error from HandleClaudeResponseData to propagate out of ClaudeHandler")
	}
	if usage != nil {
		t.Errorf("usage = %+v, want nil on error", usage)
	}
	if apiErr.GetErrorCode() != types.ErrorCodeBadResponseBody {
		t.Errorf("error code = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeBadResponseBody)
	}
}

func TestClaudeHandler_DebugEnabled_LogsRawBodyWithoutBreakingSuccess(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	prevDebug := common.DebugEnabled
	common.DebugEnabled = true
	defer func() { common.DebugEnabled = prevDebug }()

	body := `{"id":"msg_dbg","type":"message","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","model":"claude-3-opus-20240229","usage":{"input_tokens":1,"output_tokens":1}}`
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-opus-20240229"},
		RelayFormat: types.RelayFormatClaude,
	}
	usage, apiErr := ClaudeHandler(c, resp, info, RequestModeMessage)
	if apiErr != nil {
		t.Fatalf("unexpected error with DebugEnabled=true: %+v", apiErr)
	}
	if usage == nil || usage.PromptTokens != 1 {
		t.Errorf("usage = %+v, want PromptTokens=1", usage)
	}
}

// ---------------------------------------------------------------------------
// HandleStreamResponseData
// ---------------------------------------------------------------------------

func TestHandleStreamResponseData_ClaudeFormat_EchoesRawData(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
		RelayFormat: types.RelayFormatClaude,
	}
	claudeInfo := newClaudeInfo()
	data := `{"type":"message_start","message":{"id":"msg_s","model":"claude-3-opus-20240229","usage":{"input_tokens":1,"output_tokens":0}}}`

	apiErr := HandleStreamResponseData(c, info, claudeInfo, data, RequestModeMessage)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: message_start") {
		t.Errorf("body should contain the SSE event line, got %q", body)
	}
	if !strings.Contains(body, "data: "+data) {
		t.Errorf("body should echo raw data verbatim for Claude relay format, got %q", body)
	}
	// info.UpstreamModelName must be refreshed from the message_start payload
	if info.UpstreamModelName != "claude-3-opus-20240229" {
		t.Errorf("UpstreamModelName = %q, want %q", info.UpstreamModelName, "claude-3-opus-20240229")
	}
}

func TestHandleStreamResponseData_OpenAIFormat_ConvertsAndWritesChunk(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
		RelayFormat: types.RelayFormatOpenAI,
	}
	claudeInfo := newClaudeInfo()
	data := `{"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}`

	apiErr := HandleStreamResponseData(c, info, claudeInfo, data, RequestModeMessage)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"content":"hi"`) {
		t.Errorf("body should contain converted OpenAI delta content, got %q", body)
	}
	if !strings.Contains(body, `"chat.completion.chunk"`) {
		t.Errorf("body should be an OpenAI chunk object, got %q", body)
	}
}

func TestHandleStreamResponseData_OpenAIFormat_MessageStopSuppressesWrite(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
		RelayFormat: types.RelayFormatOpenAI,
	}
	claudeInfo := newClaudeInfo()
	apiErr := HandleStreamResponseData(c, info, claudeInfo, `{"type":"message_stop"}`, RequestModeMessage)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if w.Body.Len() != 0 {
		t.Errorf("message_stop should not write any SSE chunk, got %q", w.Body.String())
	}
}

func TestHandleStreamResponseData_ErrorEvent_ReturnsClaudeError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
		RelayFormat: types.RelayFormatClaude,
	}
	claudeInfo := newClaudeInfo()
	data := `{"type":"error","error":{"type":"overloaded_error","message":"upstream busy"}}`

	apiErr := HandleStreamResponseData(c, info, claudeInfo, data, RequestModeMessage)
	if apiErr == nil {
		t.Fatal("expected error for a stream error event")
	}
	if apiErr.GetErrorCode() != "overloaded_error" {
		t.Errorf("error code = %q, want %q", apiErr.GetErrorCode(), "overloaded_error")
	}
}

func TestHandleStreamResponseData_MalformedJSON_ReturnsBadResponseBody(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	claudeInfo := newClaudeInfo()

	apiErr := HandleStreamResponseData(c, info, claudeInfo, `not even json`, RequestModeMessage)
	if apiErr == nil {
		t.Fatal("expected error for malformed stream JSON")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeBadResponseBody {
		t.Errorf("error code = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeBadResponseBody)
	}
}

// ---------------------------------------------------------------------------
// HandleStreamFinalResponse
// ---------------------------------------------------------------------------

func TestHandleStreamFinalResponse_CompletionMode_EstimatesUsage(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/complete", nil)

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-2.1"},
		RelayFormat: types.RelayFormatClaude,
	}
	claudeInfo := newClaudeInfo()
	claudeInfo.ResponseText.WriteString("some completion text")

	HandleStreamFinalResponse(c, info, claudeInfo, RequestModeCompletion)
	if claudeInfo.Usage == nil || claudeInfo.Usage.CompletionTokens <= 0 {
		t.Errorf("expected estimated CompletionTokens > 0, got %+v", claudeInfo.Usage)
	}
}

func TestHandleStreamFinalResponse_MessageMode_IncompleteUsageFallsBackToEstimate(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-opus-20240229"},
		RelayFormat: types.RelayFormatClaude,
	}
	claudeInfo := newClaudeInfo()
	claudeInfo.Usage.PromptTokens = 5
	claudeInfo.Usage.CompletionTokens = 0 // incomplete -> triggers estimate fallback
	claudeInfo.Done = false
	claudeInfo.ResponseText.WriteString("streamed partial text")

	HandleStreamFinalResponse(c, info, claudeInfo, RequestModeMessage)
	if claudeInfo.Usage.CompletionTokens <= 0 {
		t.Errorf("expected fallback estimate CompletionTokens > 0, got %+v", claudeInfo.Usage)
	}
}

func TestHandleStreamFinalResponse_MessageMode_CompleteUsagePreserved(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-opus-20240229"},
		RelayFormat: types.RelayFormatClaude,
	}
	claudeInfo := newClaudeInfo()
	claudeInfo.Usage.PromptTokens = 5
	claudeInfo.Usage.CompletionTokens = 42
	claudeInfo.Done = true

	HandleStreamFinalResponse(c, info, claudeInfo, RequestModeMessage)
	if claudeInfo.Usage.CompletionTokens != 42 {
		t.Errorf("CompletionTokens = %d, want preserved 42 (usage was already complete)", claudeInfo.Usage.CompletionTokens)
	}
}

func TestHandleStreamFinalResponse_OpenAIFormat_IncludeUsageWritesFinalChunkAndDone(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	info := &relaycommon.RelayInfo{
		ChannelMeta:        &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-opus-20240229"},
		RelayFormat:        types.RelayFormatOpenAI,
		ShouldIncludeUsage: true,
	}
	claudeInfo := newClaudeInfo()
	claudeInfo.Usage.PromptTokens = 5
	claudeInfo.Usage.CompletionTokens = 3
	claudeInfo.Done = true

	HandleStreamFinalResponse(c, info, claudeInfo, RequestModeMessage)
	body := w.Body.String()
	if !strings.Contains(body, `"usage"`) {
		t.Errorf("body should contain the final usage chunk when ShouldIncludeUsage=true, got %q", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Errorf("body should end with the [DONE] terminator, got %q", body)
	}
}

func TestHandleStreamFinalResponse_OpenAIFormat_NoUsageChunkWhenNotRequested(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	info := &relaycommon.RelayInfo{
		ChannelMeta:        &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-opus-20240229"},
		RelayFormat:        types.RelayFormatOpenAI,
		ShouldIncludeUsage: false,
	}
	claudeInfo := newClaudeInfo()
	claudeInfo.Usage.PromptTokens = 5
	claudeInfo.Usage.CompletionTokens = 3
	claudeInfo.Done = true

	HandleStreamFinalResponse(c, info, claudeInfo, RequestModeMessage)
	body := w.Body.String()
	if strings.Contains(body, `"usage"`) {
		t.Errorf("body should NOT contain a usage chunk when ShouldIncludeUsage=false, got %q", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Errorf("body should still end with [DONE], got %q", body)
	}
}

func TestHandleStreamFinalResponse_ClaudeFormat_WritesNothing(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-opus-20240229"},
		RelayFormat: types.RelayFormatClaude,
	}
	claudeInfo := newClaudeInfo()
	claudeInfo.Usage.PromptTokens = 5
	claudeInfo.Usage.CompletionTokens = 3
	claudeInfo.Done = true

	HandleStreamFinalResponse(c, info, claudeInfo, RequestModeMessage)
	if w.Body.Len() != 0 {
		t.Errorf("Claude relay format should not write a synthetic final chunk, got %q", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// HandleStreamResponseData: Claude relay format dispatch on event type
// (message_start / content_block_delta / message_delta / completion mode)
// ---------------------------------------------------------------------------

func TestHandleStreamResponseData_ClaudeFormat_CompletionMode(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/complete", nil)

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-2.1"},
		RelayFormat: types.RelayFormatClaude,
	}
	claudeInfo := newClaudeInfo()
	data := `{"completion":"partial","stop_reason":""}`

	apiErr := HandleStreamResponseData(c, info, claudeInfo, data, RequestModeCompletion)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if claudeInfo.ResponseText.String() != "partial" {
		t.Errorf("ResponseText = %q, want %q (completion-mode text accumulated even under Claude relay passthrough)", claudeInfo.ResponseText.String(), "partial")
	}
	if !strings.Contains(w.Body.String(), "data: "+data) {
		t.Errorf("body should still echo the raw completion chunk, got %q", w.Body.String())
	}
}

func TestHandleStreamResponseData_ClaudeFormat_ContentBlockDeltaAndMessageDelta(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-opus-20240229"},
		RelayFormat: types.RelayFormatClaude,
	}
	claudeInfo := newClaudeInfo()

	deltaData := `{"type":"content_block_delta","delta":{"type":"text_delta","text":"chunk"}}`
	if apiErr := HandleStreamResponseData(c, info, claudeInfo, deltaData, RequestModeMessage); apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if claudeInfo.ResponseText.String() != "chunk" {
		t.Errorf("ResponseText = %q, want %q", claudeInfo.ResponseText.String(), "chunk")
	}

	msgDeltaData := `{"type":"message_delta","usage":{"input_tokens":9,"output_tokens":2}}`
	if apiErr := HandleStreamResponseData(c, info, claudeInfo, msgDeltaData, RequestModeMessage); apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if !claudeInfo.Done {
		t.Error("Done should be true after message_delta event")
	}
	if !strings.Contains(w.Body.String(), "data: "+msgDeltaData) {
		t.Errorf("body should echo the message_delta chunk, got %q", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// HandleStreamResponseData / HandleStreamFinalResponse: write-failure branches
// (client disconnected mid-stream -> gin request context cancelled)
// ---------------------------------------------------------------------------

func newCancelledCtxGinContext(w *httptest.ResponseRecorder) *gin.Context {
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	c.Request = req.WithContext(ctx)
	return c
}

func TestHandleStreamResponseData_OpenAIFormat_WriteFailureLogsButDoesNotError(t *testing.T) {
	w := httptest.NewRecorder()
	c := newCancelledCtxGinContext(w)

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
		RelayFormat: types.RelayFormatOpenAI,
	}
	claudeInfo := newClaudeInfo()
	data := `{"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}`

	// A cancelled request context makes the underlying write fail; the handler
	// must swallow that (log-only) and still return nil, not propagate a
	// NewAPIError for what is just a disconnected client.
	apiErr := HandleStreamResponseData(c, info, claudeInfo, data, RequestModeMessage)
	if apiErr != nil {
		t.Fatalf("write failure on a disconnected client must not surface as a NewAPIError, got %+v", apiErr)
	}
	if w.Body.Len() != 0 {
		t.Errorf("no bytes should have been written once the request context is cancelled, got %q", w.Body.String())
	}
}

func TestHandleStreamFinalResponse_OpenAIFormat_WriteFailureDuringUsageChunk(t *testing.T) {
	w := httptest.NewRecorder()
	c := newCancelledCtxGinContext(w)

	info := &relaycommon.RelayInfo{
		ChannelMeta:        &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-opus-20240229"},
		RelayFormat:        types.RelayFormatOpenAI,
		ShouldIncludeUsage: true,
	}
	claudeInfo := newClaudeInfo()
	claudeInfo.Usage.PromptTokens = 1
	claudeInfo.Usage.CompletionTokens = 1
	claudeInfo.Done = true

	// Must not panic even though the final usage chunk write fails.
	HandleStreamFinalResponse(c, info, claudeInfo, RequestModeMessage)
	if w.Body.Len() != 0 {
		t.Errorf("no bytes should have been written once the request context is cancelled, got %q", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// HandleStreamFinalResponse: debug-logging branch for incomplete usage
// ---------------------------------------------------------------------------

func TestHandleStreamFinalResponse_MessageMode_DebugLogsIncompleteUsage(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	prevDebug := common.DebugEnabled
	common.DebugEnabled = true
	defer func() { common.DebugEnabled = prevDebug }()

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-opus-20240229"},
		RelayFormat: types.RelayFormatClaude,
	}
	claudeInfo := newClaudeInfo()
	claudeInfo.Usage.PromptTokens = 0 // upstream never sent a prompt token count
	claudeInfo.Usage.CompletionTokens = 0
	claudeInfo.Done = false
	claudeInfo.ResponseText.WriteString("recovered from partial stream")

	// Must not panic with DebugEnabled=true exercising the extra SysLog call;
	// the estimate fallback must still populate CompletionTokens.
	HandleStreamFinalResponse(c, info, claudeInfo, RequestModeMessage)
	if claudeInfo.Usage.CompletionTokens <= 0 {
		t.Errorf("expected fallback estimate CompletionTokens > 0, got %+v", claudeInfo.Usage)
	}
}

// ---------------------------------------------------------------------------
// StreamResponseClaude2OpenAI: tool-call Index derivation, signature_delta
// ---------------------------------------------------------------------------

func TestStreamResponseClaude2OpenAI_ToolCallIndexFromResponseIndex(t *testing.T) {
	idx := 3
	claudeResp := &dto.ClaudeResponse{
		Type:         "content_block_start",
		Index:        &idx,
		ContentBlock: &dto.ClaudeMediaMessage{Type: "tool_use", Id: "toolu_x", Name: "search"},
	}
	resp := StreamResponseClaude2OpenAI(RequestModeMessage, claudeResp)
	if resp == nil {
		t.Fatal("response should not be nil")
	}
	tools := resp.Choices[0].Delta.ToolCalls
	if len(tools) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(tools))
	}
	// Index field in dto.ClaudeResponse is the 1-based content block position;
	// StreamResponseClaude2OpenAI converts it to a 0-based tool-call index (idx-1).
	if tools[0].Index == nil || *tools[0].Index != 2 {
		t.Errorf("ToolCalls[0].Index = %v, want pointer to 2 (idx-1)", tools[0].Index)
	}
}

func TestStreamResponseClaude2OpenAI_ToolCallIndexClampedAtZero(t *testing.T) {
	idx := 0
	claudeResp := &dto.ClaudeResponse{
		Type:         "content_block_start",
		Index:        &idx,
		ContentBlock: &dto.ClaudeMediaMessage{Type: "tool_use", Id: "toolu_y", Name: "search"},
	}
	resp := StreamResponseClaude2OpenAI(RequestModeMessage, claudeResp)
	tools := resp.Choices[0].Delta.ToolCalls
	if tools[0].Index == nil || *tools[0].Index != 0 {
		t.Errorf("ToolCalls[0].Index = %v, want pointer to 0 (clamped, not negative)", tools[0].Index)
	}
}

func TestStreamResponseClaude2OpenAI_SignatureDeltaSetsPlaceholderReasoning(t *testing.T) {
	claudeResp := &dto.ClaudeResponse{
		Type: "content_block_delta",
		Delta: &dto.ClaudeMediaMessage{
			Type:      "signature_delta",
			Signature: "encrypted-signature-blob",
		},
	}
	resp := StreamResponseClaude2OpenAI(RequestModeMessage, claudeResp)
	if resp == nil {
		t.Fatal("response should not be nil")
	}
	if resp.Choices[0].Delta.ReasoningContent == nil || *resp.Choices[0].Delta.ReasoningContent != "\n" {
		t.Errorf("ReasoningContent = %v, want pointer to \"\\n\" placeholder (signature content is not decoded)", resp.Choices[0].Delta.ReasoningContent)
	}
}
