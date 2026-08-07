package gemini

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	pkgconstant "github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func init() {
	// StreamScannerHandler builds a time.NewTicker(StreamingTimeout*time.Second);
	// this must be positive or the ticker construction panics. The gemini
	// package's own init/business code never sets it in a test binary.
	if pkgconstant.StreamingTimeout <= 0 {
		pkgconstant.StreamingTimeout = 60
	}
}

func respFromBody(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func newHandlerContext() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c, w
}

// ---------------------------------------------------------------------------
// GeminiChatHandler (non-streaming OpenAI-format chat)
// ---------------------------------------------------------------------------

func TestGeminiChatHandler_ValidResponse_OpenAIFormat(t *testing.T) {
	c, w := newHandlerContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"},
		RelayFormat: types.RelayFormatOpenAI,
	}
	body := `{
		"candidates": [{"content": {"parts":[{"text":"hi there"}]}, "finishReason":"STOP", "index":0}],
		"usageMetadata": {"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}
	}`
	usage, apiErr := GeminiChatHandler(c, info, respFromBody(200, body))
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.PromptTokens != 10 || usage.CompletionTokens != 5 {
		t.Errorf("usage = %+v, want prompt=10 completion=5", usage)
	}
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var got dto.OpenAITextResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("response body not valid OpenAI JSON: %v (%s)", err, w.Body.String())
	}
	if len(got.Choices) != 1 || got.Choices[0].Message.StringContent() != "hi there" {
		t.Errorf("Choices = %+v, want single 'hi there' choice", got.Choices)
	}
	if got.Model != "gemini-2.0-flash" {
		t.Errorf("Model = %q, want gemini-2.0-flash", got.Model)
	}
}

func TestGeminiChatHandler_EmptyCandidates_BlockReason(t *testing.T) {
	c, _ := newHandlerContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"candidates":[], "promptFeedback":{"blockReason":"SAFETY"}}`
	_, apiErr := GeminiChatHandler(c, info, respFromBody(200, body))
	if apiErr == nil {
		t.Fatal("expected error for blocked prompt")
	}
	if apiErr.GetErrorCode() != types.ErrorCodePromptBlocked {
		t.Errorf("ErrorCode = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodePromptBlocked)
	}
	if !strings.Contains(apiErr.Error(), "SAFETY") {
		t.Errorf("error = %q, want to mention SAFETY block reason", apiErr.Error())
	}
}

func TestGeminiChatHandler_EmptyCandidates_NoFeedback(t *testing.T) {
	c, _ := newHandlerContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"candidates":[]}`
	_, apiErr := GeminiChatHandler(c, info, respFromBody(200, body))
	if apiErr == nil {
		t.Fatal("expected error for empty response")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeEmptyResponse {
		t.Errorf("ErrorCode = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeEmptyResponse)
	}
}

func TestGeminiChatHandler_MalformedJSON(t *testing.T) {
	c, _ := newHandlerContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	_, apiErr := GeminiChatHandler(c, info, respFromBody(200, `{not json`))
	if apiErr == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeBadResponseBody {
		t.Errorf("ErrorCode = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeBadResponseBody)
	}
}

func TestGeminiChatHandler_RelayFormatGemini_PassesThroughRawBody(t *testing.T) {
	c, w := newHandlerContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
		RelayFormat: types.RelayFormatGemini,
	}
	body := `{"candidates":[{"content":{"parts":[{"text":"raw"}]},"index":0,"finishReason":null}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`
	_, apiErr := GeminiChatHandler(c, info, respFromBody(200, body))
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	// RelayFormatGemini takes the `break` branch, i.e. it forwards the
	// original upstream bytes verbatim rather than re-marshaling as OpenAI.
	var raw, out map[string]interface{}
	_ = json.Unmarshal([]byte(body), &raw)
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if _, hasChoices := out["choices"]; hasChoices {
		t.Errorf("output = %s, want raw Gemini shape (no 'choices' key) for RelayFormatGemini passthrough", w.Body.String())
	}
	if _, hasCandidates := out["candidates"]; !hasCandidates {
		t.Errorf("output = %s, want original 'candidates' key preserved", w.Body.String())
	}
}

func TestGeminiChatHandler_RelayFormatClaude_Converts(t *testing.T) {
	c, w := newHandlerContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
		RelayFormat: types.RelayFormatClaude,
	}
	body := `{"candidates":[{"content":{"parts":[{"text":"claude-ify me"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"totalTokenCount":5}}`
	_, apiErr := GeminiChatHandler(c, info, respFromBody(200, body))
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("output not valid JSON: %v (%s)", err, w.Body.String())
	}
	if out["type"] != "message" {
		t.Errorf("output type = %v, want 'message' (Claude response shape)", out["type"])
	}
}

// ---------------------------------------------------------------------------
// GeminiTextGenerationHandler (native Gemini passthrough, non-stream)
// ---------------------------------------------------------------------------

func TestGeminiTextGenerationHandler_PassesThroughAndComputesUsage(t *testing.T) {
	c, w := newHandlerContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"candidates":[{"content":{"parts":[{"text":"native"}]},"index":0,"finishReason":null}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3,"totalTokenCount":10}}`
	usage, apiErr := GeminiTextGenerationHandler(c, info, respFromBody(200, body))
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.PromptTokens != 7 || usage.CompletionTokens != 3 {
		t.Errorf("usage = %+v, want prompt=7 completion=3", usage)
	}
	if w.Body.String() != body {
		t.Errorf("body = %q, want unchanged passthrough of upstream bytes: %q", w.Body.String(), body)
	}
}

func TestGeminiTextGenerationHandler_MalformedJSON(t *testing.T) {
	c, _ := newHandlerContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	_, apiErr := GeminiTextGenerationHandler(c, info, respFromBody(200, `not json`))
	if apiErr == nil {
		t.Fatal("expected error")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeBadResponseBody {
		t.Errorf("ErrorCode = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeBadResponseBody)
	}
}

// ---------------------------------------------------------------------------
// GeminiEmbeddingHandler (OpenAI-format embedding output)
// ---------------------------------------------------------------------------

func TestGeminiEmbeddingHandler_Success(t *testing.T) {
	c, w := newHandlerContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "text-embedding-004"}}
	body := `{"embeddings":[{"values":[0.1,0.2,0.3]},{"values":[0.4,0.5]}]}`
	usage, apiErr := GeminiEmbeddingHandler(c, info, respFromBody(200, body))
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("usage is nil")
	}
	var out dto.OpenAIEmbeddingResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v (%s)", err, w.Body.String())
	}
	if len(out.Data) != 2 {
		t.Fatalf("len(Data) = %d, want 2", len(out.Data))
	}
	if out.Data[0].Index != 0 || out.Data[1].Index != 1 {
		t.Errorf("Data indices = [%d,%d], want [0,1]", out.Data[0].Index, out.Data[1].Index)
	}
	if len(out.Data[0].Embedding) != 3 || out.Data[0].Embedding[0] != 0.1 {
		t.Errorf("Data[0].Embedding = %v, want [0.1,0.2,0.3]", out.Data[0].Embedding)
	}
}

func TestGeminiEmbeddingHandler_EmptyEmbeddings(t *testing.T) {
	c, w := newHandlerContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"embeddings":[]}`
	_, apiErr := GeminiEmbeddingHandler(c, info, respFromBody(200, body))
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	var out dto.OpenAIEmbeddingResponse
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Data) != 0 {
		t.Errorf("Data = %+v, want empty", out.Data)
	}
}

func TestGeminiEmbeddingHandler_MalformedJSON(t *testing.T) {
	c, _ := newHandlerContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	_, apiErr := GeminiEmbeddingHandler(c, info, respFromBody(200, `{bad`))
	if apiErr == nil {
		t.Fatal("expected error")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeBadResponseBody {
		t.Errorf("ErrorCode = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeBadResponseBody)
	}
}

// ---------------------------------------------------------------------------
// NativeGeminiEmbeddingHandler (native passthrough for /embedContent etc.)
// ---------------------------------------------------------------------------

func TestNativeGeminiEmbeddingHandler_BatchMode(t *testing.T) {
	c, w := newHandlerContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta:            &relaycommon.ChannelMeta{},
		IsGeminiBatchEmbedding: true,
	}
	body := `{"embeddings":[{"values":[1,2]}]}`
	usage, apiErr := NativeGeminiEmbeddingHandler(c, respFromBody(200, body), info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("usage is nil")
	}
	if w.Body.String() != body {
		t.Errorf("body = %q, want unchanged passthrough %q", w.Body.String(), body)
	}
}

func TestNativeGeminiEmbeddingHandler_BatchMode_MalformedJSON(t *testing.T) {
	c, _ := newHandlerContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, IsGeminiBatchEmbedding: true}
	_, apiErr := NativeGeminiEmbeddingHandler(c, respFromBody(200, `{bad`), info)
	if apiErr == nil {
		t.Fatal("expected error for malformed batch embedding JSON")
	}
}

func TestNativeGeminiEmbeddingHandler_SingleMode(t *testing.T) {
	c, _ := newHandlerContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, IsGeminiBatchEmbedding: false}
	body := `{"embedding":{"values":[1,2,3]}}`
	usage, apiErr := NativeGeminiEmbeddingHandler(c, respFromBody(200, body), info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("usage is nil")
	}
}

func TestNativeGeminiEmbeddingHandler_SingleMode_MalformedJSON(t *testing.T) {
	c, _ := newHandlerContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, IsGeminiBatchEmbedding: false}
	_, apiErr := NativeGeminiEmbeddingHandler(c, respFromBody(200, `{bad`), info)
	if apiErr == nil {
		t.Fatal("expected error for malformed single embedding JSON")
	}
}

// ---------------------------------------------------------------------------
// GeminiImageHandler (Imagen predictions -> OpenAI image response)
// ---------------------------------------------------------------------------

func TestGeminiImageHandler_Success(t *testing.T) {
	c, w := newHandlerContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"predictions":[{"bytesBase64Encoded":"aaaa"},{"bytesBase64Encoded":"bbbb"}]}`
	usage, apiErr := GeminiImageHandler(c, info, respFromBody(200, body))
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	const imageTokens = 258
	if usage.PromptTokens != imageTokens*2 {
		t.Errorf("PromptTokens = %d, want %d (2 images x 258)", usage.PromptTokens, imageTokens*2)
	}
	if usage.CompletionTokens != 0 {
		t.Errorf("CompletionTokens = %d, want 0", usage.CompletionTokens)
	}
	var out dto.ImageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(out.Data) != 2 {
		t.Fatalf("len(Data) = %d, want 2", len(out.Data))
	}
}

func TestGeminiImageHandler_FiltersRaiFilteredPredictions(t *testing.T) {
	c, w := newHandlerContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"predictions":[{"bytesBase64Encoded":"aaaa"},{"raiFilteredReason":"blocked"}]}`
	usage, apiErr := GeminiImageHandler(c, info, respFromBody(200, body))
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.PromptTokens != 258 {
		t.Errorf("PromptTokens = %d, want 258 (only 1 unfiltered image billed)", usage.PromptTokens)
	}
	var out dto.ImageResponse
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Data) != 1 {
		t.Fatalf("len(Data) = %d, want 1 (filtered prediction excluded)", len(out.Data))
	}
}

func TestGeminiImageHandler_NoPredictions_Errors(t *testing.T) {
	c, _ := newHandlerContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	_, apiErr := GeminiImageHandler(c, info, respFromBody(200, `{"predictions":[]}`))
	if apiErr == nil {
		t.Fatal("expected error for zero predictions")
	}
	if !strings.Contains(apiErr.Error(), "no images generated") {
		t.Errorf("error = %q, want 'no images generated'", apiErr.Error())
	}
}

func TestGeminiImageHandler_MalformedJSON(t *testing.T) {
	c, _ := newHandlerContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	_, apiErr := GeminiImageHandler(c, info, respFromBody(200, `not json`))
	if apiErr == nil {
		t.Fatal("expected error")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeBadResponseBody {
		t.Errorf("ErrorCode = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeBadResponseBody)
	}
}

// ---------------------------------------------------------------------------
// Streaming handlers: GeminiChatStreamHandler / GeminiTextGenerationStreamHandler
// ---------------------------------------------------------------------------

func sseBody(frames ...string) string {
	var b strings.Builder
	for _, f := range frames {
		b.WriteString("data: ")
		b.WriteString(f)
		b.WriteString("\n\n")
	}
	return b.String()
}

func TestGeminiChatStreamHandler_TextDeltas(t *testing.T) {
	c, w := newHandlerContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
		RelayFormat: types.RelayFormatOpenAI,
	}
	body := sseBody(
		`{"candidates":[{"content":{"parts":[{"text":"Hel"}]},"index":0}]}`,
		`{"candidates":[{"content":{"parts":[{"text":"lo"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":2,"totalTokenCount":4}}`,
	)
	usage, apiErr := GeminiChatStreamHandler(c, info, respFromBody(200, body))
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.PromptTokens != 2 {
		t.Errorf("PromptTokens = %d, want 2", usage.PromptTokens)
	}
	out := w.Body.String()
	if !strings.Contains(out, `"Hel"`) || !strings.Contains(out, `"lo"`) {
		t.Errorf("stream output = %q, want to contain both text deltas", out)
	}
	if !strings.Contains(out, "[DONE]") {
		t.Errorf("stream output = %q, want terminal [DONE] marker", out)
	}
}

func TestGeminiChatStreamHandler_MalformedFrame_PropagatesError(t *testing.T) {
	c, _ := newHandlerContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, RelayFormat: types.RelayFormatOpenAI}
	body := sseBody(`{not valid json`)
	_, apiErr := GeminiChatStreamHandler(c, info, respFromBody(200, body))
	if apiErr == nil {
		t.Fatal("expected error: malformed stream frame must propagate as a real error, not be silently dropped")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeBadResponseBody {
		t.Errorf("ErrorCode = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeBadResponseBody)
	}
}

func TestGeminiChatStreamHandler_ToolCallDelta_EmitsToolCallsFinish(t *testing.T) {
	c, w := newHandlerContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, RelayFormat: types.RelayFormatOpenAI}
	body := sseBody(
		`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"f","args":{}}}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`,
	)
	_, apiErr := GeminiChatStreamHandler(c, info, respFromBody(200, body))
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	out := w.Body.String()
	if !strings.Contains(out, `"tool_calls"`) {
		t.Errorf("stream output = %q, want tool_calls finish_reason present", out)
	}
}

func TestGeminiChatStreamHandler_ImageOnly_EstimatesCompletionTokens(t *testing.T) {
	c, _ := newHandlerContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, RelayFormat: types.RelayFormatOpenAI}
	// no usageMetadata at all -> total_token_count stays 0, so the handler must
	// fall back to imageCount*1400 for CompletionTokens.
	body := sseBody(
		`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"aaaa"}}]},"finishReason":"STOP","index":0}]}`,
	)
	usage, apiErr := GeminiChatStreamHandler(c, info, respFromBody(200, body))
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.CompletionTokens != 1400 {
		t.Errorf("CompletionTokens = %d, want 1400 (1 image x 1400 fallback estimate)", usage.CompletionTokens)
	}
}

func TestGeminiTextGenerationStreamHandler_ForwardsRawFramesAndCountsResponses(t *testing.T) {
	c, w := newHandlerContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	body := sseBody(
		`{"candidates":[{"content":{"parts":[{"text":"a"}]},"index":0}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`,
		`{"candidates":[{"content":{"parts":[{"text":"b"}]},"index":0}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2,"totalTokenCount":3}}`,
	)
	usage, apiErr := GeminiTextGenerationStreamHandler(c, info, respFromBody(200, body))
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if info.SendResponseCount != 2 {
		t.Errorf("SendResponseCount = %d, want 2 (one increment per forwarded frame)", info.SendResponseCount)
	}
	if usage.PromptTokens != 1 {
		t.Errorf("PromptTokens = %d, want 1 (from last usageMetadata frame)", usage.PromptTokens)
	}
	out := w.Body.String()
	if !strings.Contains(out, `"text":"a"`) || !strings.Contains(out, `"text":"b"`) {
		t.Errorf("output = %q, want raw native frames forwarded verbatim", out)
	}
}
