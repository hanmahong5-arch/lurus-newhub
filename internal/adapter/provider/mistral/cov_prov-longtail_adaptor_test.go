package mistral

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func newProvLongtailMistralCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c, w
}

// ---------------------------------------------------------------------------
// GetRequestURL / SetupRequestHeader
// ---------------------------------------------------------------------------

func TestMistral_GetRequestURL_ComposesBaseAndPath(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.mistral.ai"},
		RequestURLPath: "/v1/chat/completions",
	}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.mistral.ai/v1/chat/completions" {
		t.Errorf("GetRequestURL = %q, want %q", got, "https://api.mistral.ai/v1/chat/completions")
	}
}

func TestMistral_SetupRequestHeader_BearerAuth(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMistralCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "mis-secret"}}
	header := http.Header{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Authorization"); got != "Bearer mis-secret" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer mis-secret")
	}
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest / ConvertRerankRequest / ModelList / ChannelName
// ---------------------------------------------------------------------------

func TestMistral_ConvertOpenAIRequest_NilRequest_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMistralCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertOpenAIRequest(c, info, nil); err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestMistral_ConvertOpenAIRequest_SimplePassThroughFields(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMistralCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	temp := 0.7
	req := &dto.GeneralOpenAIRequest{
		Model:       "mistral-large-latest",
		Temperature: &temp,
		Messages:    []dto.Message{{Role: "user", Content: "hi"}},
	}
	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := got.(*dto.GeneralOpenAIRequest)
	if out.Model != "mistral-large-latest" {
		t.Errorf("Model = %q, want mistral-large-latest", out.Model)
	}
	if out.Temperature == nil || *out.Temperature != 0.7 {
		t.Errorf("Temperature = %v, want 0.7", out.Temperature)
	}
}

func TestMistral_ConvertRerankRequest_UnsupportedNilNil(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMistralCtx()
	got, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
	if err != nil || got != nil {
		t.Errorf("ConvertRerankRequest = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestMistral_ModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	if a.GetChannelName() != "mistral" {
		t.Errorf("GetChannelName() = %q, want mistral", a.GetChannelName())
	}
	if len(a.GetModelList()) == 0 {
		t.Fatal("GetModelList() must not be empty")
	}
}

// ---------------------------------------------------------------------------
// DoResponse: stream vs. non-stream dispatch.
// ---------------------------------------------------------------------------

func TestMistral_DoResponse_NonStream_ReturnsUsage(t *testing.T) {
	a := &Adaptor{}
	c, w := newProvLongtailMistralCtx()
	info := &relaycommon.RelayInfo{IsStream: false, ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage, got %T", usage)
	}
	if u.TotalTokens != 7 {
		t.Errorf("TotalTokens = %d, want 7 (from upstream usage block)", u.TotalTokens)
	}
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// ---------------------------------------------------------------------------
// requestOpenAI2Mistral: business-critical translation. Mistral requires
// tool_call ids to be exactly 9 alphanumeric chars; ids that don't match get
// rewritten, and the same rewrite must be applied consistently to both the
// assistant's tool_calls[].id and the corresponding tool-response message's
// tool_call_id (otherwise upstream can't correlate the tool result).
// ---------------------------------------------------------------------------

func TestMistral_RequestConversion_RewritesNonConformingToolCallId_ConsistentlyAcrossMessages(t *testing.T) {
	toolCalls := []dto.ToolCallRequest{
		{ID: "call_this_id_is_too_long_and_has_underscores", Type: "function", Function: dto.FunctionRequest{Name: "get_weather"}},
	}
	tcJSON, _ := json.Marshal(toolCalls)
	assistantMsg := dto.Message{
		Role:      "assistant",
		Content:   "",
		ToolCalls: json.RawMessage(tcJSON),
	}
	toolResultMsg := dto.Message{
		Role:       "tool",
		Content:    "sunny",
		ToolCallId: "call_this_id_is_too_long_and_has_underscores",
	}
	req := &dto.GeneralOpenAIRequest{
		Model:    "mistral-large-latest",
		Messages: []dto.Message{assistantMsg, toolResultMsg},
	}

	got := requestOpenAI2Mistral(req)

	if len(got.Messages) != 2 {
		t.Fatalf("Messages len = %d, want 2", len(got.Messages))
	}
	rewrittenCalls := got.Messages[0].ParseToolCalls()
	if len(rewrittenCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(rewrittenCalls))
	}
	newID := rewrittenCalls[0].ID
	if newID == "call_this_id_is_too_long_and_has_underscores" {
		t.Fatal("non-conforming tool_call id must be rewritten to a valid 9-char id")
	}
	if !mistralToolCallIdRegexp.MatchString(newID) {
		t.Errorf("rewritten id %q does not match Mistral's required ^[a-zA-Z0-9]{9}$ pattern", newID)
	}
	if got.Messages[1].ToolCallId != newID {
		t.Errorf("tool_call_id on the follow-up tool message = %q, want the SAME rewritten id %q (correlation would break otherwise)", got.Messages[1].ToolCallId, newID)
	}
}

func TestMistral_RequestConversion_ConformingToolCallId_LeftUnchanged(t *testing.T) {
	toolCalls := []dto.ToolCallRequest{
		{ID: "abc123XYZ", Type: "function", Function: dto.FunctionRequest{Name: "f"}},
	}
	tcJSON, _ := json.Marshal(toolCalls)
	msg := dto.Message{Role: "assistant", Content: "x", ToolCalls: json.RawMessage(tcJSON)}
	req := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{msg}}

	got := requestOpenAI2Mistral(req)
	rewritten := got.Messages[0].ParseToolCalls()
	if rewritten[0].ID != "abc123XYZ" {
		t.Errorf("ID = %q, want unchanged (already conforms to the 9-char alphanumeric pattern)", rewritten[0].ID)
	}
}

func TestMistral_RequestConversion_AssistantWithToolCallsAndEmptyContent_ClearsToEmptyMediaList(t *testing.T) {
	toolCalls := []dto.ToolCallRequest{{ID: "abc123XYZ", Type: "function"}}
	tcJSON, _ := json.Marshal(toolCalls)
	msg := dto.Message{Role: "assistant", Content: "", ToolCalls: json.RawMessage(tcJSON)}
	req := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{msg}}

	got := requestOpenAI2Mistral(req)
	// SetMediaContent replaces message.Content with the (possibly empty)
	// media list, so the outgoing Content must be a []MediaContent, not the
	// original empty string.
	list, ok := got.Messages[0].Content.([]dto.MediaContent)
	if !ok {
		t.Fatalf("Content = %#v (%T), want []dto.MediaContent (tool-call-only assistant turn)", got.Messages[0].Content, got.Messages[0].Content)
	}
	if len(list) != 0 {
		t.Errorf("Content list len = %d, want 0 (assistant emitted only tool calls, no text)", len(list))
	}
}

func TestMistral_RequestConversion_ImageURL_FlattenedToPlainString(t *testing.T) {
	content := []any{
		map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": "https://example.com/cat.png", "detail": "high"},
		},
	}
	msg := dto.Message{Role: "user", Content: content}
	req := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{msg}}

	got := requestOpenAI2Mistral(req)
	list, ok := got.Messages[0].Content.([]dto.MediaContent)
	if !ok {
		t.Fatalf("Content = %#v (%T), want []dto.MediaContent", got.Messages[0].Content, got.Messages[0].Content)
	}
	if len(list) != 1 {
		t.Fatalf("Content list len = %d, want 1", len(list))
	}
	urlStr, ok := list[0].ImageUrl.(string)
	if !ok {
		t.Fatalf("ImageUrl = %#v (%T), want a flat string (Mistral rejects the structured {url,detail} form)", list[0].ImageUrl, list[0].ImageUrl)
	}
	if urlStr != "https://example.com/cat.png" {
		t.Errorf("ImageUrl = %q, want %q", urlStr, "https://example.com/cat.png")
	}
}

func TestMistral_RequestConversion_TextContent_PassesThroughAsSingleTextBlock(t *testing.T) {
	msg := dto.Message{Role: "user", Content: "hello there"}
	req := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{msg}}

	got := requestOpenAI2Mistral(req)
	list := got.Messages[0].Content.([]dto.MediaContent)
	if len(list) != 1 || list[0].Type != dto.ContentTypeText || list[0].Text != "hello there" {
		t.Errorf("Content = %+v, want single text block with the original text", list)
	}
}

func TestMistral_RequestConversion_EmptyMessages_ProducesEmptySlice(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{}}
	got := requestOpenAI2Mistral(req)
	if got.Messages == nil {
		t.Error("Messages should be an empty (non-nil) slice, not nil, for an empty input")
	}
	if len(got.Messages) != 0 {
		t.Errorf("Messages len = %d, want 0", len(got.Messages))
	}
}

func TestMistral_RequestConversion_DropsUnmappedFields(t *testing.T) {
	name := "Bob"
	msg := dto.Message{Role: "user", Content: "hi", Name: &name, ReasoningContent: "internal thoughts"}
	req := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{msg}}

	got := requestOpenAI2Mistral(req)
	// FINDING: requestOpenAI2Mistral only copies Role/Content/ToolCalls/ToolCallId
	// into the rebuilt message — Name and ReasoningContent are silently
	// dropped. Locking current behavior as the regression baseline.
	if got.Messages[0].Name != nil {
		t.Errorf("Name = %v, want nil — current implementation drops the Name field", got.Messages[0].Name)
	}
	if got.Messages[0].ReasoningContent != "" {
		t.Errorf("ReasoningContent = %q, want empty — current implementation drops it", got.Messages[0].ReasoningContent)
	}
}

func TestMistral_RequestConversion_MaxTokensAndSamplingParamsCarried(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{
		Model:     "m",
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
		MaxTokens: 256,
		TopP:      0.9,
	}
	got := requestOpenAI2Mistral(req)
	if got.GetMaxTokens() != 256 {
		t.Errorf("MaxTokens = %d, want 256", got.GetMaxTokens())
	}
	if got.TopP != 0.9 {
		t.Errorf("TopP = %v, want 0.9", got.TopP)
	}
}
