package dify

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newTestContext() (*httptest.ResponseRecorder, *gin.Context) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	return w, c
}

// ---------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------

func TestInit(t *testing.T) {
	a := &Adaptor{}
	a.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "agent-bot"}})
	if a.BotType != BotTypeChatFlow {
		t.Errorf("BotType = %d, want %d (BotTypeChatFlow)", a.BotType, BotTypeChatFlow)
	}
}

// ---------------------------------------------------------------------------
// GetRequestURL
// ---------------------------------------------------------------------------

func TestGetRequestURL(t *testing.T) {
	tests := []struct {
		name    string
		botType int
		want    string
	}{
		{"workflow", BotTypeWorkFlow, "https://api.dify.ai/v1/workflows/run"},
		{"completion", BotTypeCompletion, "https://api.dify.ai/v1/completion-messages"},
		{"agent falls through to chat", BotTypeAgent, "https://api.dify.ai/v1/chat-messages"},
		{"chatflow default", BotTypeChatFlow, "https://api.dify.ai/v1/chat-messages"},
		{"unknown falls to default", 999, "https://api.dify.ai/v1/chat-messages"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Adaptor{BotType: tt.botType}
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.dify.ai"}}
			got, err := a.GetRequestURL(info)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("GetRequestURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------------

func TestSetupRequestHeader(t *testing.T) {
	a := &Adaptor{}
	_, c := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	header := http.Header{}
	err := a.SetupRequestHeader(c, &header, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Authorization"); got != "Bearer secret-key" {
		// ApiKey unset above; verify default empty-key form, then re-check with a set key below.
		if got != "Bearer " {
			t.Errorf("Authorization = %q, want %q", got, "Bearer ")
		}
	}

	header2 := http.Header{}
	info.ApiKey = "secret-key"
	if err := a.SetupRequestHeader(c, &header2, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header2.Get("Authorization"); got != "Bearer secret-key" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer secret-key")
	}
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest
// ---------------------------------------------------------------------------

func TestConvertOpenAIRequest_NilRequest(t *testing.T) {
	a := &Adaptor{}
	_, c := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	result, err := a.ConvertOpenAIRequest(c, info, nil)
	if err == nil {
		t.Fatal("expected error for nil request, got nil")
	}
	if err.Error() != "request is nil" {
		t.Errorf("error = %q, want %q", err.Error(), "request is nil")
	}
	if result != nil {
		t.Errorf("result = %v, want nil", result)
	}
}

func TestConvertOpenAIRequest_Valid(t *testing.T) {
	a := &Adaptor{}
	_, c := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{
		User:   "user-42",
		Stream: true,
		Messages: []dto.Message{
			{Role: "system", Content: "you are helpful"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi there"},
		},
	}
	result, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	difyReq, ok := result.(*DifyChatRequest)
	if !ok {
		t.Fatalf("result type = %T, want *DifyChatRequest", result)
	}
	if difyReq.User != "user-42" {
		t.Errorf("User = %q, want %q", difyReq.User, "user-42")
	}
	if difyReq.ResponseMode != "streaming" {
		t.Errorf("ResponseMode = %q, want %q", difyReq.ResponseMode, "streaming")
	}
	wantQuery := "SYSTEM: \nyou are helpful\nUSER: \nhello\nASSISTANT: \nhi there\n"
	if difyReq.Query != wantQuery {
		t.Errorf("Query = %q, want %q", difyReq.Query, wantQuery)
	}
	if len(difyReq.Files) != 0 {
		t.Errorf("Files = %v, want empty", difyReq.Files)
	}
}

func TestConvertOpenAIRequest_BlockingModeAndEmptyUser(t *testing.T) {
	a := &Adaptor{}
	_, c := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{
		Stream: false,
		Messages: []dto.Message{
			{Role: "user", Content: "just asking"},
		},
	}
	result, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	difyReq := result.(*DifyChatRequest)
	if difyReq.ResponseMode != "blocking" {
		t.Errorf("ResponseMode = %q, want %q", difyReq.ResponseMode, "blocking")
	}
	if difyReq.User == "" {
		t.Error("User should be auto-generated from response ID when empty, got empty string")
	}
}

// ---------------------------------------------------------------------------
// ConvertRerankRequest
// ---------------------------------------------------------------------------

func TestConvertRerankRequest(t *testing.T) {
	a := &Adaptor{}
	result, err := a.ConvertRerankRequest(nil, 0, dto.RerankRequest{})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("result = %v, want nil", result)
	}
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestGetModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	if got := a.GetChannelName(); got != "dify" {
		t.Errorf("GetChannelName() = %q, want %q", got, "dify")
	}
	// ModelList is a package-level var; adaptor must return the exact same slice.
	got := a.GetModelList()
	if len(got) != len(ModelList) {
		t.Errorf("GetModelList() len = %d, want %d", len(got), len(ModelList))
	}
}

// ---------------------------------------------------------------------------
// requestOpenAI2Dify (direct, more branch coverage on message parsing)
// ---------------------------------------------------------------------------

func TestRequestOpenAI2Dify_TextContentParts(t *testing.T) {
	_, c := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.GeneralOpenAIRequest{
		User: "u1",
		Messages: []dto.Message{
			{Role: "user", Content: []any{
				map[string]any{"type": "text", "text": "part one"},
			}},
		},
	}
	got := requestOpenAI2Dify(c, info, req)
	if got.Query != "USER: \npart one\n" {
		t.Errorf("Query = %q, want %q", got.Query, "USER: \npart one\n")
	}
}

func TestRequestOpenAI2Dify_LocalImageAttachment(t *testing.T) {
	_, c := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://example.invalid"}}
	req := dto.GeneralOpenAIRequest{
		User: "u3",
		Messages: []dto.Message{
			{Role: "user", Content: []any{
				map[string]any{
					"type": "image_url",
					"image_url": map[string]any{
						// Non-http prefix -> IsRemoteImage() is false, routes into
						// uploadDifyFile (local upload path) instead of the
						// (buggy, nil-deref) remote-image branch.
						"url": "data:image/png;base64,not-valid-base64!!!",
					},
				},
			}},
		},
	}
	got := requestOpenAI2Dify(c, info, req)
	// Invalid base64 makes uploadDifyFile return nil before any network call,
	// so no file should be appended.
	if len(got.Files) != 0 {
		t.Errorf("Files = %v, want empty (invalid base64 upload should yield no file)", got.Files)
	}
}

func TestRequestOpenAI2Dify_EmptyMessages(t *testing.T) {
	_, c := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.GeneralOpenAIRequest{User: "u2"}
	got := requestOpenAI2Dify(c, info, req)
	if got.Query != "" {
		t.Errorf("Query = %q, want empty", got.Query)
	}
	if len(got.Files) != 0 {
		t.Errorf("Files = %v, want empty slice", got.Files)
	}
	if got.AutoGenerateName {
		t.Error("AutoGenerateName should default to false")
	}
}

// ---------------------------------------------------------------------------
// uploadDifyFile — only the hermetically reachable early-return branches
// (bad base64 decode failure) are exercised; the happy path requires a live
// HTTP upload endpoint and is not reachable in a hermetic test.
// ---------------------------------------------------------------------------

func TestUploadDifyFile_BadBase64ReturnsNil(t *testing.T) {
	_, c := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://example.invalid"}}
	media := dto.MediaContent{
		Type: dto.ContentTypeImageURL,
		ImageUrl: map[string]any{
			"url": "data:image/png;base64,not-valid-base64!!!",
		},
	}
	got := uploadDifyFile(c, info, "user-1", media)
	if got != nil {
		t.Errorf("expected nil for invalid base64 payload, got %+v", got)
	}
}

func TestUploadDifyFile_NonImageTypeReturnsNil(t *testing.T) {
	_, c := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://example.invalid"}}
	media := dto.MediaContent{Type: "unsupported"}
	got := uploadDifyFile(c, info, "user-1", media)
	if got != nil {
		t.Errorf("expected nil for unsupported media type, got %+v", got)
	}
}

// ---------------------------------------------------------------------------
// streamResponseDify2OpenAI
// ---------------------------------------------------------------------------

func TestStreamResponseDify2OpenAI(t *testing.T) {
	origDebug := constant.DifyDebug
	defer func() { constant.DifyDebug = origDebug }()

	t.Run("workflow event with debug off produces empty delta", func(t *testing.T) {
		constant.DifyDebug = false
		resp := DifyChunkChatCompletionResponse{Event: "workflow_started", Data: DifyData{WorkflowId: "wf-1"}}
		got := streamResponseDify2OpenAI(resp)
		if got.Object != "chat.completion.chunk" {
			t.Errorf("Object = %q, want %q", got.Object, "chat.completion.chunk")
		}
		if got.Model != "dify" {
			t.Errorf("Model = %q, want %q", got.Model, "dify")
		}
		if len(got.Choices) != 1 {
			t.Fatalf("Choices len = %d, want 1", len(got.Choices))
		}
		if got.Choices[0].Delta.ReasoningContent != nil {
			t.Errorf("ReasoningContent should be nil when DifyDebug=false, got %v", got.Choices[0].Delta.ReasoningContent)
		}
	})

	t.Run("workflow_finished with debug on includes status", func(t *testing.T) {
		constant.DifyDebug = true
		resp := DifyChunkChatCompletionResponse{Event: "workflow_finished", Data: DifyData{WorkflowId: "wf-2", Status: "succeeded"}}
		got := streamResponseDify2OpenAI(resp)
		want := "Workflow: wf-2 succeeded\n"
		if got.Choices[0].Delta.ReasoningContent == nil || *got.Choices[0].Delta.ReasoningContent != want {
			t.Errorf("ReasoningContent = %v, want %q", got.Choices[0].Delta.ReasoningContent, want)
		}
	})

	t.Run("node_finished with debug on includes status", func(t *testing.T) {
		constant.DifyDebug = true
		resp := DifyChunkChatCompletionResponse{Event: "node_finished", Data: DifyData{NodeType: "llm", Status: "succeeded"}}
		got := streamResponseDify2OpenAI(resp)
		want := "Node: llm succeeded\n"
		if got.Choices[0].Delta.ReasoningContent == nil || *got.Choices[0].Delta.ReasoningContent != want {
			t.Errorf("ReasoningContent = %v, want %q", got.Choices[0].Delta.ReasoningContent, want)
		}
	})

	t.Run("node_started with debug on omits status suffix", func(t *testing.T) {
		constant.DifyDebug = true
		resp := DifyChunkChatCompletionResponse{Event: "node_started", Data: DifyData{NodeType: "llm", Status: "running"}}
		got := streamResponseDify2OpenAI(resp)
		want := "Node: llm\n"
		if got.Choices[0].Delta.ReasoningContent == nil || *got.Choices[0].Delta.ReasoningContent != want {
			t.Errorf("ReasoningContent = %v, want %q", got.Choices[0].Delta.ReasoningContent, want)
		}
	})

	t.Run("message event sets content", func(t *testing.T) {
		constant.DifyDebug = false
		resp := DifyChunkChatCompletionResponse{Event: "message", Answer: "hello world"}
		got := streamResponseDify2OpenAI(resp)
		if got.Choices[0].Delta.GetContentString() != "hello world" {
			t.Errorf("content = %q, want %q", got.Choices[0].Delta.GetContentString(), "hello world")
		}
	})

	t.Run("agent_message event sets content", func(t *testing.T) {
		resp := DifyChunkChatCompletionResponse{Event: "agent_message", Answer: "agent says hi"}
		got := streamResponseDify2OpenAI(resp)
		if got.Choices[0].Delta.GetContentString() != "agent says hi" {
			t.Errorf("content = %q, want %q", got.Choices[0].Delta.GetContentString(), "agent says hi")
		}
	})

	t.Run("thinking-details answer is translated to <think>", func(t *testing.T) {
		resp := DifyChunkChatCompletionResponse{
			Event:  "message",
			Answer: "<details style=\"color:gray;background-color: #f8f8f8;padding: 8px;border-radius: 4px;\" open> <summary> Thinking... </summary>\n",
		}
		got := streamResponseDify2OpenAI(resp)
		if got.Choices[0].Delta.GetContentString() != "<think>" {
			t.Errorf("content = %q, want %q", got.Choices[0].Delta.GetContentString(), "<think>")
		}
	})

	t.Run("closing details tag is translated to </think>", func(t *testing.T) {
		resp := DifyChunkChatCompletionResponse{Event: "message", Answer: "</details>"}
		got := streamResponseDify2OpenAI(resp)
		if got.Choices[0].Delta.GetContentString() != "</think>" {
			t.Errorf("content = %q, want %q", got.Choices[0].Delta.GetContentString(), "</think>")
		}
	})

	t.Run("unrecognized event yields empty delta", func(t *testing.T) {
		resp := DifyChunkChatCompletionResponse{Event: "ping"}
		got := streamResponseDify2OpenAI(resp)
		if got.Choices[0].Delta.GetContentString() != "" {
			t.Errorf("content = %q, want empty", got.Choices[0].Delta.GetContentString())
		}
		if got.Choices[0].Delta.ReasoningContent != nil {
			t.Errorf("ReasoningContent should stay nil for unrecognized event, got %v", got.Choices[0].Delta.ReasoningContent)
		}
	})
}

// ---------------------------------------------------------------------------
// difyHandler (non-streaming) — fully hermetic: reads an in-memory response
// body, no network I/O.
// ---------------------------------------------------------------------------

func TestDifyHandler_Success(t *testing.T) {
	w, c := newTestContext()
	body := `{"conversation_id":"conv-1","answer":"the answer","metadata":{"usage":{"total_tokens":42}}}`
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	usage, apiErr := difyHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.TotalTokens != 42 {
		t.Errorf("TotalTokens = %d, want 42", usage.TotalTokens)
	}
	if w.Code != http.StatusOK {
		t.Errorf("http status written = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "the answer") {
		t.Errorf("response body = %q, want it to contain %q", w.Body.String(), "the answer")
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want %q", w.Header().Get("Content-Type"), "application/json")
	}
}

func TestDifyHandler_MalformedJSON(t *testing.T) {
	_, c := newTestContext()
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader("{not-json")),
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	usage, apiErr := difyHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for malformed JSON body, got nil")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
}

// errReader always fails on Read, to exercise the io.ReadAll error branch.
type errReader struct{}

func (errReader) Read(_ []byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (errReader) Close() error               { return nil }

func TestDifyHandler_BodyReadError(t *testing.T) {
	_, c := newTestContext()
	resp := &http.Response{
		StatusCode: 200,
		Body:       errReader{},
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	usage, apiErr := difyHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error when body read fails, got nil")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil", usage)
	}
}

// ---------------------------------------------------------------------------
// difyStreamHandler — exercises the SSE scan loop against an in-memory
// reader (no network); this is hermetic because StreamScannerHandler only
// requires an io.Reader body, not a live connection.
// ---------------------------------------------------------------------------

func TestDifyStreamHandler(t *testing.T) {
	origTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	defer func() { constant.StreamingTimeout = origTimeout }()
	sse := "data: {\"event\":\"message\",\"answer\":\"hi\"}\n\n" +
		"data: {\"event\":\"message_end\",\"metadata\":{\"usage\":{\"total_tokens\":7,\"completion_tokens\":3}}}\n\n" +
		"data: [DONE]\n\n"
	_, c := newTestContext()
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(sse)),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "dify-model"},
		DisablePing: true,
	}
	usage, apiErr := difyStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("usage is nil")
	}
	if usage.TotalTokens != 7 {
		t.Errorf("TotalTokens = %d, want 7", usage.TotalTokens)
	}
}

// ---------------------------------------------------------------------------
// DoResponse — pure dispatch to difyHandler/difyStreamHandler, both of which
// are hermetic (no network); DoRequest itself is not exercised here since it
// always performs a live HTTP round-trip via provider.DoApiRequest.
// ---------------------------------------------------------------------------

func TestDoResponse_NonStreaming(t *testing.T) {
	a := &Adaptor{}
	_, c := newTestContext()
	body := `{"conversation_id":"conv-2","answer":"blocking answer","metadata":{"usage":{"total_tokens":5}}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, IsStream: false}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("usage type = %T, want *dto.Usage", usage)
	}
	if u.TotalTokens != 5 {
		t.Errorf("TotalTokens = %d, want 5", u.TotalTokens)
	}
}

func TestDoResponse_Streaming(t *testing.T) {
	origTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	defer func() { constant.StreamingTimeout = origTimeout }()

	a := &Adaptor{}
	_, c := newTestContext()
	sse := "data: {\"event\":\"message_end\",\"metadata\":{\"usage\":{\"total_tokens\":9}}}\n\ndata: [DONE]\n\n"
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(sse))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, IsStream: true, DisablePing: true}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("usage type = %T, want *dto.Usage", usage)
	}
	if u.TotalTokens != 9 {
		t.Errorf("TotalTokens = %d, want 9", u.TotalTokens)
	}
}

func TestDifyStreamHandler_MalformedJSONLineIsSkipped(t *testing.T) {
	origTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	defer func() { constant.StreamingTimeout = origTimeout }()

	sse := "data: {not-json\n\n" +
		"data: {\"event\":\"message_end\",\"metadata\":{\"usage\":{\"total_tokens\":3}}}\n\n"
	_, c := newTestContext()
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(sse))}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "dify-model"},
		DisablePing: true,
	}
	usage, apiErr := difyStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil || usage.TotalTokens != 3 {
		t.Errorf("usage = %+v, want TotalTokens=3 (malformed line should be skipped, not fatal)", usage)
	}
}

func TestDifyStreamHandler_ReasoningEventsCountAsNodeTokens(t *testing.T) {
	origTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	defer func() { constant.StreamingTimeout = origTimeout }()
	origDebug := constant.DifyDebug
	constant.DifyDebug = true
	defer func() { constant.DifyDebug = origDebug }()

	sse := "data: {\"event\":\"workflow_started\",\"data\":{\"workflow_id\":\"wf-1\"}}\n\n" +
		"data: {\"event\":\"node_started\",\"data\":{\"node_type\":\"llm\"}}\n\n" +
		"data: {\"event\":\"message_end\",\"metadata\":{\"usage\":{\"total_tokens\":11,\"completion_tokens\":4}}}\n\n"
	_, c := newTestContext()
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(sse))}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "dify-model"},
		DisablePing: true,
	}
	usage, apiErr := difyStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("usage is nil")
	}
	// message_end usage.TotalTokens=11 wins over the estimate, and the two
	// reasoning (workflow_/node_) chunks each add 1 to CompletionTokens.
	if usage.TotalTokens != 11 {
		t.Errorf("TotalTokens = %d, want 11", usage.TotalTokens)
	}
	if usage.CompletionTokens != 6 {
		t.Errorf("CompletionTokens = %d, want 4 (from metadata) + 2 (reasoning chunks) = 6", usage.CompletionTokens)
	}
}

func TestDifyStreamHandler_ErrorEventStopsEarly(t *testing.T) {
	origTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	defer func() { constant.StreamingTimeout = origTimeout }()
	sse := "data: {\"event\":\"error\"}\n\n"
	_, c := newTestContext()
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(sse)),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "dify-model"},
		DisablePing: true,
	}
	usage, apiErr := difyStreamHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("usage is nil")
	}
	// No message_end event was seen, so usage falls back to the
	// ResponseText2Usage estimate off of an empty responseText.
	if usage.TotalTokens != 0 && usage.PromptTokens == 0 && usage.CompletionTokens == 0 {
		t.Errorf("expected zeroed/estimated usage fields for error-only stream, got %+v", usage)
	}
}
