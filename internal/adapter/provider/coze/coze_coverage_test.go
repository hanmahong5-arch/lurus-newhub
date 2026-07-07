package coze

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/gin-gonic/gin"
)

func newTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	return c, w
}

func TestGetRequestURL(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.coze.cn"},
	}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://api.coze.cn/v3/chat"
	if got != want {
		t.Errorf("GetRequestURL() = %q, want %q", got, want)
	}
}

func TestSetupRequestHeader(t *testing.T) {
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-test-key"},
	}
	header := http.Header{}

	a := &Adaptor{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Authorization"); got != "Bearer sk-test-key" {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer sk-test-key")
	}
}

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("nil request returns error", func(t *testing.T) {
		got, err := a.ConvertOpenAIRequest(nil, nil, nil)
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "request is nil" {
			t.Errorf("error = %v, want %q", err, "request is nil")
		}
	})

	t.Run("non-nil request is converted to CozeChatRequest", func(t *testing.T) {
		c, _ := newTestContext()
		c.Set("bot_id", "bot-123")

		req := &dto.GeneralOpenAIRequest{
			Model:  "moonshot-v1-8k",
			Stream: true,
			User:   "explicit-user",
			Messages: []dto.Message{
				{Role: "system", Content: "be nice"},
				{Role: "user", Content: "hello there"},
				{Role: "assistant", Content: "hi"},
				{Role: "user", Content: "second question"},
			},
		}

		got, err := a.ConvertOpenAIRequest(c, nil, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cozeReq, ok := got.(*CozeChatRequest)
		if !ok {
			t.Fatalf("expected *CozeChatRequest, got %T", got)
		}
		if cozeReq.BotId != "bot-123" {
			t.Errorf("BotId = %q, want %q", cozeReq.BotId, "bot-123")
		}
		if cozeReq.UserId != "explicit-user" {
			t.Errorf("UserId = %q, want %q", cozeReq.UserId, "explicit-user")
		}
		if !cozeReq.Stream {
			t.Errorf("Stream = false, want true")
		}
		// only "user" role messages should be kept, system/assistant filtered out
		if len(cozeReq.AdditionalMessages) != 2 {
			t.Fatalf("AdditionalMessages length = %d, want 2", len(cozeReq.AdditionalMessages))
		}
		if cozeReq.AdditionalMessages[0].Content != "hello there" {
			t.Errorf("AdditionalMessages[0].Content = %v, want %q", cozeReq.AdditionalMessages[0].Content, "hello there")
		}
		if cozeReq.AdditionalMessages[0].Role != "user" {
			t.Errorf("AdditionalMessages[0].Role = %q, want %q", cozeReq.AdditionalMessages[0].Role, "user")
		}
		if cozeReq.AdditionalMessages[0].ContentType != "text" {
			t.Errorf("AdditionalMessages[0].ContentType = %q, want %q", cozeReq.AdditionalMessages[0].ContentType, "text")
		}
		if cozeReq.AdditionalMessages[1].Content != "second question" {
			t.Errorf("AdditionalMessages[1].Content = %v, want %q", cozeReq.AdditionalMessages[1].Content, "second question")
		}
	})

	t.Run("empty user falls back to GetResponseID", func(t *testing.T) {
		c, _ := newTestContext()
		req := &dto.GeneralOpenAIRequest{
			Messages: []dto.Message{{Role: "user", Content: "hi"}},
		}
		got, err := a.ConvertOpenAIRequest(c, nil, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		cozeReq := got.(*CozeChatRequest)
		want := "chatcmpl-"
		if len(cozeReq.UserId) < len(want) || cozeReq.UserId[:len(want)] != want {
			t.Errorf("UserId = %q, want prefix %q", cozeReq.UserId, want)
		}
	})
}

func TestNotImplementedStubs(t *testing.T) {
	a := &Adaptor{}

	t.Run("ConvertGeminiRequest", func(t *testing.T) {
		got, err := a.ConvertGeminiRequest(nil, nil, nil)
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("error = %v, want %q", err, "not implemented")
		}
	})

	t.Run("ConvertAudioRequest", func(t *testing.T) {
		got, err := a.ConvertAudioRequest(nil, nil, dto.AudioRequest{})
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("error = %v, want %q", err, "not implemented")
		}
	})

	t.Run("ConvertClaudeRequest", func(t *testing.T) {
		got, err := a.ConvertClaudeRequest(nil, nil, nil)
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("error = %v, want %q", err, "not implemented")
		}
	})

	t.Run("ConvertEmbeddingRequest", func(t *testing.T) {
		got, err := a.ConvertEmbeddingRequest(nil, nil, dto.EmbeddingRequest{})
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("error = %v, want %q", err, "not implemented")
		}
	})

	t.Run("ConvertImageRequest", func(t *testing.T) {
		got, err := a.ConvertImageRequest(nil, nil, dto.ImageRequest{})
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("error = %v, want %q", err, "not implemented")
		}
	})

	t.Run("ConvertOpenAIResponsesRequest", func(t *testing.T) {
		got, err := a.ConvertOpenAIResponsesRequest(nil, nil, dto.OpenAIResponsesRequest{})
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("error = %v, want %q", err, "not implemented")
		}
	})
}

func TestConvertRerankRequest(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertRerankRequest(nil, 0, dto.RerankRequest{})
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err == nil || err.Error() != "not implemented" {
		t.Errorf("error = %v, want %q", err, "not implemented")
	}
}

func TestInit(t *testing.T) {
	a := &Adaptor{}
	// Init is a no-op; calling it must not panic and must accept a nil info.
	a.Init(nil)
}

func TestGetModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}

	gotModels := a.GetModelList()
	if len(gotModels) != len(ModelList) {
		t.Fatalf("GetModelList() length = %d, want %d", len(gotModels), len(ModelList))
	}
	for i, m := range ModelList {
		if gotModels[i] != m {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, gotModels[i], m)
		}
	}
	// spot-check a couple of specific entries to lock in the exact contract
	wantFirst := "moonshot-v1-8k"
	wantLast := "Doubao-1.5-pro-256k"
	if gotModels[0] != wantFirst {
		t.Errorf("GetModelList()[0] = %q, want %q", gotModels[0], wantFirst)
	}
	if gotModels[len(gotModels)-1] != wantLast {
		t.Errorf("GetModelList()[last] = %q, want %q", gotModels[len(gotModels)-1], wantLast)
	}

	if got := a.GetChannelName(); got != "coze" {
		t.Errorf("GetChannelName() = %q, want %q", got, "coze")
	}
}

func TestCozeChatHandler_Success(t *testing.T) {
	c, w := newTestContext()
	c.Set("coze_input_count", 3)
	c.Set("coze_output_count", 5)
	c.Set("coze_token_count", 8)

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "moonshot-v1-8k"},
	}

	detail := CozeChatDetailResponse{
		Code: 0,
		Data: []CozeChatV3MessageDetail{
			{Type: "verbose", Content: json.RawMessage(`"ignored"`)},
			{Type: "answer", Content: json.RawMessage(`"final answer"`), CreatedAt: 1234},
		},
	}
	body, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal detail: %v", err)
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(body)),
	}

	usage, apiErr := cozeChatHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.PromptTokens != 3 || usage.CompletionTokens != 5 || usage.TotalTokens != 8 {
		t.Errorf("usage = %+v, want {3 5 8}", usage)
	}

	var got dto.TextResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal written response: %v, body=%s", err, w.Body.String())
	}
	if got.Model != "moonshot-v1-8k" {
		t.Errorf("Model = %q, want %q", got.Model, "moonshot-v1-8k")
	}
	if len(got.Choices) != 1 {
		t.Fatalf("Choices length = %d, want 1", len(got.Choices))
	}
	if got.Choices[0].FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", got.Choices[0].FinishReason, "stop")
	}
	content, ok := got.Choices[0].Message.Content.(string)
	if !ok || content != "final answer" {
		t.Errorf("Message.Content = %v, want %q", got.Choices[0].Message.Content, "final answer")
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want %q", w.Header().Get("Content-Type"), "application/json")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestCozeChatHandler_ErrorCode(t *testing.T) {
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	detail := CozeChatDetailResponse{Code: 500, Msg: "boom"}
	body, _ := json.Marshal(detail)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(body)),
	}

	usage, apiErr := cozeChatHandler(c, info, resp)
	if usage != nil {
		t.Errorf("expected nil usage, got %+v", usage)
	}
	if apiErr == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCozeChatHandler_BadJSON(t *testing.T) {
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader([]byte("not json"))),
	}

	usage, apiErr := cozeChatHandler(c, info, resp)
	if usage != nil {
		t.Errorf("expected nil usage, got %+v", usage)
	}
	if apiErr == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCozeChatStreamHandler(t *testing.T) {
	c, w := newTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "moonshot-v1-8k"},
	}

	deltaData, _ := json.Marshal(CozeChatV3MessageDetail{
		Type:    "answer",
		Content: json.RawMessage(`"hello world"`),
	})
	completedData, _ := json.Marshal(CozeChatResponseData{
		Usage: CozeChatUsage{TokenCount: 10, InputCount: 4, OutputCount: 6},
	})
	errorData, _ := json.Marshal(CozeError{Code: 1, Message: "oops"})

	stream := "" +
		"event: conversation.message.delta\n" +
		"data: " + string(deltaData) + "\n" +
		"\n" +
		"event: error\n" +
		"data: " + string(errorData) + "\n" +
		"\n" +
		"event: conversation.chat.completed\n" +
		"data: " + string(completedData) + "\n" +
		"\n"

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader([]byte(stream))),
	}

	usage, apiErr := cozeChatStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.PromptTokens != 4 || usage.CompletionTokens != 6 || usage.TotalTokens != 10 {
		t.Errorf("usage = %+v, want {4 6 10}", usage)
	}
	if w.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type = %q, want %q", w.Header().Get("Content-Type"), "text/event-stream")
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("hello world")) {
		t.Errorf("expected written body to contain delta content, got: %s", w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("[DONE]")) {
		t.Errorf("expected written body to contain [DONE] terminator, got: %s", w.Body.String())
	}
}

func TestCozeChatStreamHandler_FallsBackToEstimatedUsage(t *testing.T) {
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "moonshot-v1-8k"},
	}
	c.Set("coze_input_count", 2)

	deltaData, _ := json.Marshal(CozeChatV3MessageDetail{
		Type:    "answer",
		Content: json.RawMessage(`"some content"`),
	})
	stream := "event: conversation.message.delta\ndata: " + string(deltaData) + "\n\n"

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader([]byte(stream))),
	}

	usage, apiErr := cozeChatStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	// No "conversation.chat.completed" event fired -> TotalTokens stays 0 from
	// the SSE parsing, so the handler falls back to local estimation.
	if usage.TotalTokens == 0 {
		t.Errorf("expected estimated usage.TotalTokens > 0, got 0")
	}
	if usage.PromptTokens != 2 {
		t.Errorf("PromptTokens = %d, want 2 (from coze_input_count)", usage.PromptTokens)
	}
}

func TestHandleCozeEvent_MalformedPayloadsDoNotPanic(t *testing.T) {
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	usage := &dto.Usage{}
	var responseText string

	// Malformed JSON for each branch must be swallowed (logged) not panic.
	handleCozeEvent(c, "conversation.chat.completed", "not json", &responseText, usage, "id-1", info)
	handleCozeEvent(c, "conversation.message.delta", "not json", &responseText, usage, "id-1", info)
	handleCozeEvent(c, "error", "not json", &responseText, usage, "id-1", info)
	// message.delta with valid detail JSON but Content that isn't a valid
	// JSON string (still shouldn't panic).
	handleCozeEvent(c, "conversation.message.delta", `{"content":123}`, &responseText, usage, "id-1", info)
	// unknown event type is a no-op
	handleCozeEvent(c, "unknown.event", `{}`, &responseText, usage, "id-1", info)

	if responseText != "" {
		t.Errorf("responseText = %q, want empty (no valid delta processed)", responseText)
	}
}

func TestDoResponse_RoutesByStreamFlag(t *testing.T) {
	a := &Adaptor{}

	t.Run("non-stream", func(t *testing.T) {
		c, _ := newTestContext()
		info := &relaycommon.RelayInfo{
			IsStream:    false,
			ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "moonshot-v1-8k"},
		}
		detail := CozeChatDetailResponse{Code: 0}
		body, _ := json.Marshal(detail)
		resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body))}

		_, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
	})

	t.Run("stream", func(t *testing.T) {
		c, _ := newTestContext()
		info := &relaycommon.RelayInfo{
			IsStream:    true,
			ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "moonshot-v1-8k"},
		}
		resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader([]byte{}))}

		_, apiErr := a.DoResponse(c, resp, info)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
	})
}
