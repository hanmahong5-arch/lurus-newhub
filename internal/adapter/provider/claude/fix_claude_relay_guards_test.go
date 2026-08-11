package claude

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// fixClaudeGuardsContext 构造一个最小可用的 gin 上下文（无网络、无 DB）。
func fixClaudeGuardsContext() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	return c, w
}

func fixClaudeGuardsUpstreamResp() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("")),
	}
}

func fixClaudeGuardsInfo(format types.RelayFormat, model string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayFormat: format,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: model},
	}
}

func fixClaudeGuardsResponseInfo() *ClaudeResponseInfo {
	return &ClaudeResponseInfo{Usage: &dto.Usage{}}
}

// ---------------------------------------------------------------------------
// HandleClaudeResponseData — completion 模式无顶层 usage（relay-claude.go:762）
// ---------------------------------------------------------------------------

// 旧代码在 requestMode 分支之后无条件解引用 claudeResponse.Usage.ServerToolUse，
// 而 /v1/complete 的响应体没有 "usage" 字段（Usage 为 omitempty 指针 => nil），
// 因此本用例在修复前必然 nil-panic。
func TestFixClaudeHandleResponseData_CompletionModeWithoutUsage(t *testing.T) {
	c, w := fixClaudeGuardsContext()
	info := fixClaudeGuardsInfo(types.RelayFormatClaude, "claude-2.1")
	claudeInfo := fixClaudeGuardsResponseInfo()
	body := []byte(`{"completion":" Hello there","stop_reason":"stop_sequence","model":"claude-2.1"}`)

	upstream := fixClaudeGuardsUpstreamResp()
	defer func() { _ = upstream.Body.Close() }()

	apiErr := HandleClaudeResponseData(c, info, claudeInfo, upstream, body, RequestModeCompletion)
	if apiErr != nil {
		t.Fatalf("expected no error for completion response without usage, got %v", apiErr)
	}
	if claudeInfo.Usage == nil || claudeInfo.Usage.CompletionTokens <= 0 {
		t.Fatalf("expected estimated completion tokens, got %+v", claudeInfo.Usage)
	}
	if w.Body.String() != string(body) {
		t.Errorf("body = %q, want %q", w.Body.String(), string(body))
	}
	if _, exists := c.Get("claude_web_search_requests"); exists {
		t.Error("claude_web_search_requests should not be set when usage is absent")
	}
}

// OpenAI 出参格式下的同一条 completion 路径（转换分支不同，nil usage 解引用点相同）。
func TestFixClaudeHandleResponseData_CompletionModeWithoutUsage_OpenAIFormat(t *testing.T) {
	c, w := fixClaudeGuardsContext()
	info := fixClaudeGuardsInfo(types.RelayFormatOpenAI, "claude-instant-1.2")
	claudeInfo := fixClaudeGuardsResponseInfo()
	body := []byte(`{"completion":" hi","stop_reason":"stop_sequence","model":"claude-instant-1.2"}`)

	upstream := fixClaudeGuardsUpstreamResp()
	defer func() { _ = upstream.Body.Close() }()

	apiErr := HandleClaudeResponseData(c, info, claudeInfo, upstream, body, RequestModeCompletion)
	if apiErr != nil {
		t.Fatalf("expected no error, got %v", apiErr)
	}
	if w.Body.Len() == 0 {
		t.Error("expected converted OpenAI response to be written")
	}
}

// ---------------------------------------------------------------------------
// HandleClaudeResponseData — message 模式缺 usage（relay-claude.go:741-747）
// ---------------------------------------------------------------------------

// 旧代码直接 claudeResponse.Usage.InputTokens，成功结构但缺 "usage" 的上游响应
// 会 nil-panic（无本地 recover，最终由全局 recover 中间件变成 500）。
func TestFixClaudeHandleResponseData_MessageModeMissingUsage(t *testing.T) {
	c, _ := fixClaudeGuardsContext()
	info := fixClaudeGuardsInfo(types.RelayFormatClaude, "claude-sonnet-4-5-20250929")
	claudeInfo := fixClaudeGuardsResponseInfo()
	body := []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5-20250929","content":[{"type":"text","text":"hi"}]}`)

	upstream := fixClaudeGuardsUpstreamResp()
	defer func() { _ = upstream.Body.Close() }()

	apiErr := HandleClaudeResponseData(c, info, claudeInfo, upstream, body, RequestModeMessage)
	if apiErr == nil {
		t.Fatal("expected a bad_response_body error when upstream omits usage")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeBadResponseBody {
		t.Errorf("error code = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeBadResponseBody)
	}
}

// 正常 message 响应（带 usage + server_tool_use）行为保持不变。
func TestFixClaudeHandleResponseData_MessageModeWithUsageUnchanged(t *testing.T) {
	c, w := fixClaudeGuardsContext()
	info := fixClaudeGuardsInfo(types.RelayFormatClaude, "claude-sonnet-4-5-20250929")
	claudeInfo := fixClaudeGuardsResponseInfo()
	body := []byte(`{"id":"msg_2","type":"message","role":"assistant","model":"claude-sonnet-4-5-20250929",` +
		`"content":[{"type":"text","text":"hi"}],` +
		`"usage":{"input_tokens":11,"output_tokens":7,"cache_read_input_tokens":3,"cache_creation_input_tokens":2,` +
		`"server_tool_use":{"web_search_requests":4}}}`)

	upstream := fixClaudeGuardsUpstreamResp()
	defer func() { _ = upstream.Body.Close() }()

	apiErr := HandleClaudeResponseData(c, info, claudeInfo, upstream, body, RequestModeMessage)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if claudeInfo.Usage.PromptTokens != 11 || claudeInfo.Usage.CompletionTokens != 7 || claudeInfo.Usage.TotalTokens != 18 {
		t.Errorf("usage = %+v, want prompt=11 completion=7 total=18", claudeInfo.Usage)
	}
	if claudeInfo.Usage.PromptTokensDetails.CachedTokens != 3 {
		t.Errorf("cached tokens = %d, want 3", claudeInfo.Usage.PromptTokensDetails.CachedTokens)
	}
	if claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens != 2 {
		t.Errorf("cached creation tokens = %d, want 2", claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens)
	}
	got, exists := c.Get("claude_web_search_requests")
	if !exists || got != 4 {
		t.Errorf("claude_web_search_requests = %v (exists=%v), want 4", got, exists)
	}
	if w.Body.String() != string(body) {
		t.Errorf("body = %q, want passthrough", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// FormatClaudeResponseInfo / 流式事件缺字段（relay-claude.go:595-605）
// ---------------------------------------------------------------------------

// 旧代码在 message_start 分支直接解引用 claudeResponse.Message 与 Message.Usage，
// 两者都是 omitempty 指针，缺失即 panic（被 SafeGo 吞掉 => SSE 静默挂死）。
func TestFixClaudeFormatResponseInfo_MessageStartMissingFields(t *testing.T) {
	tests := []struct {
		name string
		resp *dto.ClaudeResponse
	}{
		{
			name: "message missing",
			resp: &dto.ClaudeResponse{Type: "message_start"},
		},
		{
			name: "usage missing",
			resp: &dto.ClaudeResponse{Type: "message_start", Message: &dto.ClaudeMediaMessage{Id: "msg_x", Model: "claude-3-opus-20240229"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claudeInfo := fixClaudeGuardsResponseInfo()
			if FormatClaudeResponseInfo(RequestModeMessage, tt.resp, nil, claudeInfo) {
				t.Error("expected false for an incomplete message_start event")
			}
			if claudeInfo.Usage.PromptTokens != 0 || claudeInfo.Usage.CompletionTokens != 0 {
				t.Errorf("usage should stay zero, got %+v", claudeInfo.Usage)
			}
		})
	}
}

// 完整的 message_start 仍按原逻辑取 usage。
func TestFixClaudeFormatResponseInfo_MessageStartComplete(t *testing.T) {
	claudeInfo := fixClaudeGuardsResponseInfo()
	resp := &dto.ClaudeResponse{
		Type: "message_start",
		Message: &dto.ClaudeMediaMessage{
			Id:    "msg_ok",
			Model: "claude-3-opus-20240229",
			Usage: &dto.ClaudeUsage{InputTokens: 9, OutputTokens: 1, CacheReadInputTokens: 2},
		},
	}
	if !FormatClaudeResponseInfo(RequestModeMessage, resp, nil, claudeInfo) {
		t.Fatal("expected true for a complete message_start event")
	}
	if claudeInfo.ResponseId != "msg_ok" || claudeInfo.Model != "claude-3-opus-20240229" {
		t.Errorf("responseId/model = %q/%q", claudeInfo.ResponseId, claudeInfo.Model)
	}
	if claudeInfo.Usage.PromptTokens != 9 || claudeInfo.Usage.CompletionTokens != 1 {
		t.Errorf("usage = %+v, want prompt=9 completion=1", claudeInfo.Usage)
	}
}

// message_delta 缺 usage：旧代码解引用 claudeResponse.Usage 直接 panic。
func TestFixClaudeFormatResponseInfo_MessageDeltaMissingUsage(t *testing.T) {
	claudeInfo := fixClaudeGuardsResponseInfo()
	resp := &dto.ClaudeResponse{Type: "message_delta"}
	if !FormatClaudeResponseInfo(RequestModeMessage, resp, nil, claudeInfo) {
		t.Fatal("expected true for message_delta")
	}
	if !claudeInfo.Done {
		t.Error("Done should be marked even when usage is absent")
	}
	if claudeInfo.Usage.CompletionTokens != 0 {
		t.Errorf("completion tokens = %d, want 0", claudeInfo.Usage.CompletionTokens)
	}
}

// content_block_delta 缺 delta：旧代码解引用 claudeResponse.Delta 直接 panic。
func TestFixClaudeFormatResponseInfo_ContentBlockDeltaMissingDelta(t *testing.T) {
	claudeInfo := fixClaudeGuardsResponseInfo()
	resp := &dto.ClaudeResponse{Type: "content_block_delta"}
	if FormatClaudeResponseInfo(RequestModeMessage, resp, nil, claudeInfo) {
		t.Error("expected false for a content_block_delta event without delta")
	}
	if claudeInfo.ResponseText.Len() != 0 {
		t.Errorf("response text = %q, want empty", claudeInfo.ResponseText.String())
	}
}

// StreamResponseClaude2OpenAI 在 OpenAI 出参路径上先于 FormatClaudeResponseInfo 执行，
// 旧代码同样无条件读取 claudeResponse.Message.Id。
func TestFixClaudeStreamResponse2OpenAI_MessageStartMissingMessage(t *testing.T) {
	resp := StreamResponseClaude2OpenAI(RequestModeMessage, &dto.ClaudeResponse{Type: "message_start"})
	if resp != nil {
		t.Errorf("expected nil response for message_start without message, got %+v", resp)
	}
}

// HandleStreamResponseData 的 Claude 出参分支另有一处 claudeResponse.Message.Model。
func TestFixClaudeHandleStreamResponseData_MessageStartMissingMessage(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {
		c, _ := fixClaudeGuardsContext()
		info := fixClaudeGuardsInfo(format, "claude-sonnet-4-5-20250929")
		claudeInfo := fixClaudeGuardsResponseInfo()

		apiErr := HandleStreamResponseData(c, info, claudeInfo, `{"type":"message_start"}`, RequestModeMessage)
		if apiErr != nil {
			t.Fatalf("[%s] unexpected error: %v", format, apiErr)
		}
		if info.UpstreamModelName != "claude-sonnet-4-5-20250929" {
			t.Errorf("[%s] upstream model = %q, want unchanged", format, info.UpstreamModelName)
		}
	}
}

// ---------------------------------------------------------------------------
// RequestOpenAI2ClaudeMessage — 空 role 默认值（relay-claude.go:250-253）
// ---------------------------------------------------------------------------

// 旧代码只写回 textRequest.Messages[i].Role，range 拷贝 message.Role 仍是 ""，
// 于是空 role 被原样发往上游，并额外触发"首条必须是 user"的占位消息注入。
func TestFixClaudeRequestOpenAI2ClaudeMessage_EmptyRoleDefaultsToUser(t *testing.T) {
	c, _ := fixClaudeGuardsContext()
	req := dto.GeneralOpenAIRequest{
		Model:    "claude-sonnet-4-5-20250929",
		Messages: []dto.Message{{Content: "hello"}},
	}

	claudeReq, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(claudeReq.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1 (no placeholder message injected): %+v", len(claudeReq.Messages), claudeReq.Messages)
	}
	if claudeReq.Messages[0].Role != "user" {
		t.Errorf("Role = %q, want %q", claudeReq.Messages[0].Role, "user")
	}
	if content, ok := claudeReq.Messages[0].Content.(string); !ok || content != "hello" {
		t.Errorf("Content = %v, want %q", claudeReq.Messages[0].Content, "hello")
	}
	if req.Messages[0].Role != "user" {
		t.Errorf("source message role = %q, want %q", req.Messages[0].Role, "user")
	}
}

// 显式 role 不受影响。
func TestFixClaudeRequestOpenAI2ClaudeMessage_ExplicitRolesPreserved(t *testing.T) {
	c, _ := fixClaudeGuardsContext()
	req := dto.GeneralOpenAIRequest{
		Model: "claude-sonnet-4-5-20250929",
		Messages: []dto.Message{
			{Role: "user", Content: "q"},
			{Role: "assistant", Content: "a"},
		},
	}

	claudeReq, err := RequestOpenAI2ClaudeMessage(c, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(claudeReq.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2: %+v", len(claudeReq.Messages), claudeReq.Messages)
	}
	if claudeReq.Messages[0].Role != "user" || claudeReq.Messages[1].Role != "assistant" {
		t.Errorf("roles = %q/%q, want user/assistant", claudeReq.Messages[0].Role, claudeReq.Messages[1].Role)
	}
}
