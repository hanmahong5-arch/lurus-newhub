package dto

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// ---------- channel_settings.go ----------

func TestChannelOtherSettings_IsOpenRouterEnterprise(t *testing.T) {
	var nilSettings *ChannelOtherSettings
	if nilSettings.IsOpenRouterEnterprise() {
		t.Errorf("nil receiver should return false")
	}

	empty := &ChannelOtherSettings{}
	if empty.IsOpenRouterEnterprise() {
		t.Errorf("nil OpenRouterEnterprise field should return false")
	}

	trueVal := true
	withTrue := &ChannelOtherSettings{OpenRouterEnterprise: &trueVal}
	if !withTrue.IsOpenRouterEnterprise() {
		t.Errorf("expected true when OpenRouterEnterprise=true")
	}

	falseVal := false
	withFalse := &ChannelOtherSettings{OpenRouterEnterprise: &falseVal}
	if withFalse.IsOpenRouterEnterprise() {
		t.Errorf("expected false when OpenRouterEnterprise=false")
	}
}

// ---------- error.go ----------

func TestGeneralErrorResponse_TryToOpenAIError(t *testing.T) {
	// empty Error -> nil
	empty := GeneralErrorResponse{}
	if got := empty.TryToOpenAIError(); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}

	// valid OpenAI error object
	withErr := GeneralErrorResponse{Error: json.RawMessage(`{"message":"bad thing","type":"invalid_request_error"}`)}
	got := withErr.TryToOpenAIError()
	if got == nil || got.Message != "bad thing" || got.Type != "invalid_request_error" {
		t.Errorf("expected parsed error, got %+v", got)
	}

	// Error present but message empty -> nil
	noMsg := GeneralErrorResponse{Error: json.RawMessage(`{"type":"x"}`)}
	if got := noMsg.TryToOpenAIError(); got != nil {
		t.Errorf("expected nil when message empty, got %+v", got)
	}

	// malformed JSON -> nil
	bad := GeneralErrorResponse{Error: json.RawMessage(`not json`)}
	if got := bad.TryToOpenAIError(); got != nil {
		t.Errorf("expected nil for malformed json, got %+v", got)
	}
}

func TestGeneralErrorResponse_ToMessage(t *testing.T) {
	tests := []struct {
		name string
		resp GeneralErrorResponse
		want string
	}{
		{
			name: "error object with message",
			resp: GeneralErrorResponse{Error: json.RawMessage(`{"message":"obj err"}`)},
			want: "obj err",
		},
		{
			name: "error string",
			resp: GeneralErrorResponse{Error: json.RawMessage(`"str err"`)},
			want: "str err",
		},
		{
			name: "error other type (number) falls to default branch",
			resp: GeneralErrorResponse{Error: json.RawMessage(`42`)},
			want: "42",
		},
		{
			name: "top level Message field",
			resp: GeneralErrorResponse{Message: "top message"},
			want: "top message",
		},
		{
			name: "Msg field",
			resp: GeneralErrorResponse{Msg: "msg field"},
			want: "msg field",
		},
		{
			name: "Err field",
			resp: GeneralErrorResponse{Err: "err field"},
			want: "err field",
		},
		{
			name: "ErrorMsg field",
			resp: GeneralErrorResponse{ErrorMsg: "error_msg field"},
			want: "error_msg field",
		},
		{
			name: "nothing set",
			resp: GeneralErrorResponse{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.resp.ToMessage(); got != tt.want {
				t.Errorf("ToMessage() = %q, want %q", got, tt.want)
			}
		})
	}

	// Header.Message branch
	var withHeader GeneralErrorResponse
	withHeader.Header.Message = "header message"
	if got := withHeader.ToMessage(); got != "header message" {
		t.Errorf("Header.Message branch: got %q", got)
	}

	// Response.Error.Message branch
	var withResponse GeneralErrorResponse
	withResponse.Response.Error.Message = "response message"
	if got := withResponse.ToMessage(); got != "response message" {
		t.Errorf("Response.Error.Message branch: got %q", got)
	}

	// object error but empty message falls through to Message field
	fallthroughCase := GeneralErrorResponse{
		Error:   json.RawMessage(`{"message":""}`),
		Message: "fallback",
	}
	if got := fallthroughCase.ToMessage(); got != "fallback" {
		t.Errorf("expected fallback to Message field, got %q", got)
	}

	// string error but empty string falls through
	fallthroughStrCase := GeneralErrorResponse{
		Error: json.RawMessage(`""`),
		Msg:   "fallback-msg",
	}
	if got := fallthroughStrCase.ToMessage(); got != "fallback-msg" {
		t.Errorf("expected fallback to Msg field, got %q", got)
	}
}

// ---------- embedding.go ----------

func TestEmbeddingRequest_ParseInput(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  []string
	}{
		{name: "nil input", input: nil, want: []string{}},
		{name: "string input", input: "hello", want: []string{"hello"}},
		{name: "array of strings", input: []any{"a", "b"}, want: []string{"a", "b"}},
		{name: "array with non-string filtered", input: []any{"a", 1, "b"}, want: []string{"a", "b"}},
		{name: "unsupported type", input: 42, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &EmbeddingRequest{Input: tt.input}
			got := r.ParseInput()
			if len(got) != len(tt.want) {
				t.Fatalf("ParseInput() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ParseInput()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestEmbeddingRequest_GetTokenCountMeta(t *testing.T) {
	r := &EmbeddingRequest{Input: []any{"one", "two"}}
	meta := r.GetTokenCountMeta()
	if meta.CombineText != "one\ntwo" {
		t.Errorf("CombineText = %q, want %q", meta.CombineText, "one\ntwo")
	}
}

func TestEmbeddingRequest_IsStream(t *testing.T) {
	r := &EmbeddingRequest{}
	if r.IsStream(nil) {
		t.Errorf("EmbeddingRequest.IsStream should always be false")
	}
}

func TestEmbeddingRequest_SetModelName(t *testing.T) {
	r := &EmbeddingRequest{Model: "orig"}
	r.SetModelName("")
	if r.Model != "orig" {
		t.Errorf("empty modelName should not overwrite, got %q", r.Model)
	}
	r.SetModelName("new-model")
	if r.Model != "new-model" {
		t.Errorf("Model = %q, want new-model", r.Model)
	}
}

// ---------- audio.go ----------

func TestAudioRequest_GetTokenCountMeta(t *testing.T) {
	gptReq := &AudioRequest{Model: "gpt-4o-audio", Input: "hi"}
	meta := gptReq.GetTokenCountMeta()
	if meta.TokenType != types.TokenTypeTokenizer {
		t.Errorf("gpt model should use tokenizer type, got %v", meta.TokenType)
	}
	if meta.CombineText != "hi" {
		t.Errorf("CombineText = %q, want hi", meta.CombineText)
	}

	nonGpt := &AudioRequest{Model: "whisper-1", Input: "hi"}
	meta2 := nonGpt.GetTokenCountMeta()
	if meta2.TokenType != types.TokenTypeTextNumber {
		t.Errorf("non-gpt model should use text_number type, got %v", meta2.TokenType)
	}
}

func TestAudioRequest_IsStream(t *testing.T) {
	sse := &AudioRequest{StreamFormat: "sse"}
	if !sse.IsStream(nil) {
		t.Errorf("expected true for sse stream format")
	}
	other := &AudioRequest{StreamFormat: "audio"}
	if other.IsStream(nil) {
		t.Errorf("expected false for non-sse stream format")
	}
}

func TestAudioRequest_SetModelName(t *testing.T) {
	r := &AudioRequest{Model: "orig"}
	r.SetModelName("")
	if r.Model != "orig" {
		t.Errorf("empty modelName should not overwrite")
	}
	r.SetModelName("tts-1")
	if r.Model != "tts-1" {
		t.Errorf("Model = %q, want tts-1", r.Model)
	}
}

// ---------- rerank.go ----------

func TestRerankRequest_IsStream(t *testing.T) {
	r := &RerankRequest{}
	if r.IsStream(nil) {
		t.Errorf("RerankRequest.IsStream should always be false")
	}
}

func TestRerankRequest_GetTokenCountMeta(t *testing.T) {
	r := &RerankRequest{Documents: []any{"doc1", 2, "doc3"}, Query: "q"}
	meta := r.GetTokenCountMeta()
	want := "doc1\n2\ndoc3\nq"
	if meta.CombineText != want {
		t.Errorf("CombineText = %q, want %q", meta.CombineText, want)
	}

	noQuery := &RerankRequest{Documents: []any{"a"}}
	meta2 := noQuery.GetTokenCountMeta()
	if meta2.CombineText != "a" {
		t.Errorf("CombineText = %q, want %q", meta2.CombineText, "a")
	}
}

func TestRerankRequest_SetModelName(t *testing.T) {
	r := &RerankRequest{Model: "orig"}
	r.SetModelName("")
	if r.Model != "orig" {
		t.Errorf("empty modelName should not overwrite")
	}
	r.SetModelName("rerank-v2")
	if r.Model != "rerank-v2" {
		t.Errorf("Model = %q, want rerank-v2", r.Model)
	}
}

func TestRerankRequest_GetReturnDocuments(t *testing.T) {
	r := &RerankRequest{}
	if r.GetReturnDocuments() {
		t.Errorf("nil ReturnDocuments should return false")
	}
	trueVal := true
	r2 := &RerankRequest{ReturnDocuments: &trueVal}
	if !r2.GetReturnDocuments() {
		t.Errorf("expected true")
	}
	falseVal := false
	r3 := &RerankRequest{ReturnDocuments: &falseVal}
	if r3.GetReturnDocuments() {
		t.Errorf("expected false")
	}
}

// ---------- notify.go ----------

func TestNewNotify(t *testing.T) {
	values := []interface{}{"a", 1}
	n := NewNotify(NotifyTypeQuotaExceed, "title", "content", values)
	if n.Type != NotifyTypeQuotaExceed || n.Title != "title" || n.Content != "content" {
		t.Errorf("NewNotify fields mismatch: %+v", n)
	}
	if len(n.Values) != 2 || n.Values[0] != "a" || n.Values[1] != 1 {
		t.Errorf("NewNotify Values mismatch: %+v", n.Values)
	}
}

// ---------- request_common.go ----------

func TestBaseRequest(t *testing.T) {
	b := &BaseRequest{}
	meta := b.GetTokenCountMeta()
	if meta.TokenType != types.TokenTypeTokenizer {
		t.Errorf("BaseRequest.GetTokenCountMeta() TokenType = %v, want %v", meta.TokenType, types.TokenTypeTokenizer)
	}
	if b.IsStream(nil) {
		t.Errorf("BaseRequest.IsStream should always be false")
	}
	// SetModelName is a no-op; calling it must not panic.
	b.SetModelName("anything")
}

// ---------- openai_video.go ----------

func TestOpenAIVideo_SetProgressStr(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{name: "with percent suffix", input: "42%", want: 42},
		{name: "without percent suffix", input: "10", want: 10},
		{name: "invalid string yields zero", input: "abc", want: 0},
		{name: "empty string", input: "", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &OpenAIVideo{}
			v.SetProgressStr(tt.input)
			if v.Progress != tt.want {
				t.Errorf("Progress = %d, want %d", v.Progress, tt.want)
			}
		})
	}
}

func TestOpenAIVideo_SetMetadata(t *testing.T) {
	v := &OpenAIVideo{}
	if v.Metadata != nil {
		t.Fatalf("expected nil metadata initially")
	}
	v.SetMetadata("key1", "value1")
	if v.Metadata["key1"] != "value1" {
		t.Errorf("Metadata[key1] = %v, want value1", v.Metadata["key1"])
	}
	v.SetMetadata("key2", 2)
	if len(v.Metadata) != 2 {
		t.Errorf("expected 2 metadata entries, got %d", len(v.Metadata))
	}
}

func TestNewOpenAIVideo(t *testing.T) {
	v := NewOpenAIVideo()
	if v.Object != "video" {
		t.Errorf("Object = %q, want video", v.Object)
	}
}

// ---------- gemini.go ----------

func TestGeminiChatRequest_UnmarshalJSON(t *testing.T) {
	// camelCase systemInstruction
	var camel GeminiChatRequest
	if err := json.Unmarshal([]byte(`{"systemInstruction":{"role":"system","parts":[{"text":"camel"}]}}`), &camel); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if camel.SystemInstructions == nil || camel.SystemInstructions.Role != "system" {
		t.Errorf("expected camelCase systemInstruction parsed, got %+v", camel.SystemInstructions)
	}

	// snake_case system_instruction takes priority
	var snake GeminiChatRequest
	if err := json.Unmarshal([]byte(`{"systemInstruction":{"role":"a"},"system_instruction":{"role":"b"}}`), &snake); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if snake.SystemInstructions == nil || snake.SystemInstructions.Role != "b" {
		t.Errorf("expected snake_case to win, got %+v", snake.SystemInstructions)
	}

	// malformed json returns error
	var bad GeminiChatRequest
	if err := json.Unmarshal([]byte(`not json`), &bad); err == nil {
		t.Errorf("expected error for malformed json")
	}
}

func TestGeminiChatRequest_GetTokenCountMeta(t *testing.T) {
	r := &GeminiChatRequest{
		GenerationConfig: GeminiChatGenerationConfig{MaxOutputTokens: 100},
		Contents: []GeminiChatContent{
			{
				Parts: []GeminiPart{
					{Text: "hello"},
					{InlineData: &GeminiInlineData{MimeType: "image/png", Data: "imgdata"}},
					{InlineData: &GeminiInlineData{MimeType: "audio/mp3", Data: "auddata"}},
					{InlineData: &GeminiInlineData{MimeType: "video/mp4", Data: "viddata"}},
					{InlineData: &GeminiInlineData{MimeType: "application/pdf", Data: "filedata"}},
				},
			},
		},
	}
	meta := r.GetTokenCountMeta()
	if meta.CombineText != "hello" {
		t.Errorf("CombineText = %q, want hello", meta.CombineText)
	}
	if meta.MaxTokens != 100 {
		t.Errorf("MaxTokens = %d, want 100", meta.MaxTokens)
	}
	if len(meta.Files) != 4 {
		t.Fatalf("expected 4 files, got %d: %+v", len(meta.Files), meta.Files)
	}
	wantTypes := []types.FileType{types.FileTypeImage, types.FileTypeAudio, types.FileTypeVideo, types.FileTypeFile}
	for i, f := range meta.Files {
		if f.FileType != wantTypes[i] {
			t.Errorf("Files[%d].FileType = %v, want %v", i, f.FileType, wantTypes[i])
		}
	}
}

func TestGeminiChatRequest_IsStream(t *testing.T) {
	r := &GeminiChatRequest{}
	gin.SetMode(gin.TestMode)

	recSSE := httptest.NewRecorder()
	ctxSSE, _ := gin.CreateTestContext(recSSE)
	ctxSSE.Request = httptest.NewRequest(http.MethodGet, "/x?alt=sse", nil)
	if !r.IsStream(ctxSSE) {
		t.Errorf("expected true when alt=sse")
	}

	recOther := httptest.NewRecorder()
	ctxOther, _ := gin.CreateTestContext(recOther)
	ctxOther.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	if r.IsStream(ctxOther) {
		t.Errorf("expected false without alt=sse")
	}
}

func TestGeminiChatRequest_SetModelName_NoOp(t *testing.T) {
	r := &GeminiChatRequest{}
	// Method is documented as a no-op; must not panic.
	r.SetModelName("anything")
}

func TestGeminiChatRequest_GetSetTools(t *testing.T) {
	r := &GeminiChatRequest{}
	if got := r.GetTools(); got != nil {
		t.Errorf("expected nil tools for empty Tools, got %+v", got)
	}

	// array form
	r.Tools = json.RawMessage(`[{"googleSearch":{}}]`)
	tools := r.GetTools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	// object form
	r.Tools = json.RawMessage(`{"codeExecution":{}}`)
	tools2 := r.GetTools()
	if len(tools2) != 1 {
		t.Fatalf("expected 1 tool from object form, got %d", len(tools2))
	}

	// malformed array
	r.Tools = json.RawMessage(`[bad`)
	if got := r.GetTools(); got != nil {
		t.Errorf("expected nil for malformed array tools, got %+v", got)
	}

	// malformed object
	r.Tools = json.RawMessage(`{bad`)
	if got := r.GetTools(); got != nil {
		t.Errorf("expected nil for malformed object tools, got %+v", got)
	}

	// neither array nor object (e.g. a bare string) -> nil
	r.Tools = json.RawMessage(`"weird"`)
	if got := r.GetTools(); got != nil {
		t.Errorf("expected nil for non-array/object tools, got %+v", got)
	}

	// SetTools with empty slice
	r.SetTools(nil)
	if string(r.Tools) != "[]" {
		t.Errorf("SetTools(nil) = %q, want []", string(r.Tools))
	}

	// SetTools with content round-trips
	r.SetTools([]GeminiChatTool{{GoogleSearch: "on"}})
	roundTrip := r.GetTools()
	if len(roundTrip) != 1 {
		t.Fatalf("expected 1 tool after SetTools round-trip, got %d", len(roundTrip))
	}
}

func TestGeminiThinkingConfig_UnmarshalJSON(t *testing.T) {
	var camel GeminiThinkingConfig
	budget := 50
	if err := json.Unmarshal([]byte(`{"includeThoughts":true,"thinkingBudget":50,"thinkingLevel":"high"}`), &camel); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !camel.IncludeThoughts || camel.ThinkingBudget == nil || *camel.ThinkingBudget != budget || camel.ThinkingLevel != "high" {
		t.Errorf("camelCase parse mismatch: %+v", camel)
	}

	var snake GeminiThinkingConfig
	if err := json.Unmarshal([]byte(`{"include_thoughts":true,"thinking_budget":30,"thinking_level":"low"}`), &snake); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !snake.IncludeThoughts || snake.ThinkingBudget == nil || *snake.ThinkingBudget != 30 || snake.ThinkingLevel != "low" {
		t.Errorf("snake_case parse mismatch: %+v", snake)
	}

	var bad GeminiThinkingConfig
	if err := json.Unmarshal([]byte(`not json`), &bad); err == nil {
		t.Errorf("expected error for malformed json")
	}
}

func TestGeminiThinkingConfig_SetThinkingBudget(t *testing.T) {
	c := &GeminiThinkingConfig{}
	c.SetThinkingBudget(77)
	if c.ThinkingBudget == nil || *c.ThinkingBudget != 77 {
		t.Errorf("ThinkingBudget = %v, want 77", c.ThinkingBudget)
	}
}

func TestGeminiInlineData_UnmarshalJSON(t *testing.T) {
	var camel GeminiInlineData
	if err := json.Unmarshal([]byte(`{"mimeType":"image/png","data":"d1"}`), &camel); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if camel.MimeType != "image/png" || camel.Data != "d1" {
		t.Errorf("camelCase parse mismatch: %+v", camel)
	}

	var snake GeminiInlineData
	if err := json.Unmarshal([]byte(`{"mime_type":"audio/mp3","data":"d2"}`), &snake); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if snake.MimeType != "audio/mp3" || snake.Data != "d2" {
		t.Errorf("snake_case parse mismatch: %+v", snake)
	}

	// both present: snake_case wins
	var both GeminiInlineData
	if err := json.Unmarshal([]byte(`{"mimeType":"a","mime_type":"b","data":"d3"}`), &both); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if both.MimeType != "b" {
		t.Errorf("expected snake_case to win, got %q", both.MimeType)
	}

	var bad GeminiInlineData
	if err := json.Unmarshal([]byte(`not json`), &bad); err == nil {
		t.Errorf("expected error for malformed json")
	}
}

func TestGeminiPart_UnmarshalJSON(t *testing.T) {
	var camel GeminiPart
	if err := json.Unmarshal([]byte(`{"text":"hi","inlineData":{"mimeType":"image/png","data":"d1"}}`), &camel); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if camel.Text != "hi" || camel.InlineData == nil || camel.InlineData.MimeType != "image/png" {
		t.Errorf("camelCase parse mismatch: %+v", camel)
	}

	var snake GeminiPart
	if err := json.Unmarshal([]byte(`{"text":"hi","inline_data":{"mimeType":"image/jpeg","data":"d2"}}`), &snake); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if snake.InlineData == nil || snake.InlineData.MimeType != "image/jpeg" {
		t.Errorf("snake_case parse mismatch: %+v", snake)
	}

	var noInline GeminiPart
	if err := json.Unmarshal([]byte(`{"text":"hi"}`), &noInline); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if noInline.InlineData != nil {
		t.Errorf("expected nil InlineData when absent")
	}

	var bad GeminiPart
	if err := json.Unmarshal([]byte(`not json`), &bad); err == nil {
		t.Errorf("expected error for malformed json")
	}
}

func TestGeminiEmbeddingRequest(t *testing.T) {
	r := &GeminiEmbeddingRequest{
		Content: GeminiChatContent{Parts: []GeminiPart{{Text: "a"}, {Text: "b"}, {Text: ""}}},
	}
	if r.IsStream(nil) {
		t.Errorf("GeminiEmbeddingRequest.IsStream should always be false")
	}
	meta := r.GetTokenCountMeta()
	if meta.CombineText != "a\nb" {
		t.Errorf("CombineText = %q, want a\\nb", meta.CombineText)
	}
	r.SetModelName("")
	if r.Model != "" {
		t.Errorf("empty modelName should not set Model")
	}
	r.SetModelName("embed-1")
	if r.Model != "embed-1" {
		t.Errorf("Model = %q, want embed-1", r.Model)
	}
}

func TestGeminiBatchEmbeddingRequest(t *testing.T) {
	r := &GeminiBatchEmbeddingRequest{
		Requests: []*GeminiEmbeddingRequest{
			{Content: GeminiChatContent{Parts: []GeminiPart{{Text: "x"}}}},
			{Content: GeminiChatContent{Parts: []GeminiPart{{Text: "y"}}}},
		},
	}
	if r.IsStream(nil) {
		t.Errorf("GeminiBatchEmbeddingRequest.IsStream should always be false")
	}
	meta := r.GetTokenCountMeta()
	if meta.CombineText != "x\ny" {
		t.Errorf("CombineText = %q, want x\\ny", meta.CombineText)
	}
	r.SetModelName("batch-model")
	for _, req := range r.Requests {
		if req.Model != "batch-model" {
			t.Errorf("expected sub-request model set, got %q", req.Model)
		}
	}

	// empty requests slice: CombineText should be empty
	empty := &GeminiBatchEmbeddingRequest{}
	emptyMeta := empty.GetTokenCountMeta()
	if emptyMeta.CombineText != "" {
		t.Errorf("expected empty CombineText, got %q", emptyMeta.CombineText)
	}
	// SetModelName with empty modelName should not panic and not set anything (no-op loop)
	empty.SetModelName("")
}

// ---------- openai_response.go ----------

func TestGetOpenAIError(t *testing.T) {
	if got := GetOpenAIError(nil); got != nil {
		t.Errorf("expected nil for nil error field, got %+v", got)
	}

	structErr := types.OpenAIError{Message: "m1", Type: "t1"}
	if got := GetOpenAIError(structErr); got == nil || got.Message != "m1" {
		t.Errorf("struct case mismatch: %+v", got)
	}

	ptrErr := &types.OpenAIError{Message: "m2"}
	if got := GetOpenAIError(ptrErr); got != ptrErr {
		t.Errorf("pointer case should return same pointer")
	}

	mapErr := map[string]interface{}{
		"type":    "invalid_request_error",
		"message": "bad request",
		"param":   "model",
		"code":    "400",
	}
	got := GetOpenAIError(mapErr)
	if got == nil || got.Type != "invalid_request_error" || got.Message != "bad request" || got.Param != "model" || got.Code != "400" {
		t.Errorf("map case mismatch: %+v", got)
	}

	strErr := "plain string error"
	got2 := GetOpenAIError(strErr)
	if got2 == nil || got2.Type != "error" || got2.Message != strErr {
		t.Errorf("string case mismatch: %+v", got2)
	}

	got3 := GetOpenAIError(12345)
	if got3 == nil || got3.Type != "unknown_error" || got3.Message != "12345" {
		t.Errorf("default case mismatch: %+v", got3)
	}
}

func TestSimpleResponse_GetOpenAIError(t *testing.T) {
	s := &SimpleResponse{Error: "boom"}
	got := s.GetOpenAIError()
	if got == nil || got.Message != "boom" {
		t.Errorf("SimpleResponse.GetOpenAIError mismatch: %+v", got)
	}
}

func TestOpenAITextResponse_GetOpenAIError(t *testing.T) {
	o := &OpenAITextResponse{Error: map[string]interface{}{"message": "fail"}}
	got := o.GetOpenAIError()
	if got == nil || got.Message != "fail" {
		t.Errorf("OpenAITextResponse.GetOpenAIError mismatch: %+v", got)
	}
	// no error set
	o2 := &OpenAITextResponse{}
	if got := o2.GetOpenAIError(); got != nil {
		t.Errorf("expected nil when Error unset, got %+v", got)
	}
}

func TestChatCompletionsStreamResponseChoiceDelta(t *testing.T) {
	d := &ChatCompletionsStreamResponseChoiceDelta{}
	if d.GetContentString() != "" {
		t.Errorf("expected empty content string initially")
	}
	d.SetContentString("hello")
	if d.GetContentString() != "hello" {
		t.Errorf("GetContentString() = %q, want hello", d.GetContentString())
	}

	if d.GetReasoningContent() != "" {
		t.Errorf("expected empty reasoning content initially")
	}
	d.SetReasoningContent("thinking...")
	if d.GetReasoningContent() != "thinking..." {
		t.Errorf("GetReasoningContent() = %q, want thinking...", d.GetReasoningContent())
	}

	// Reasoning field fallback (ReasoningContent nil, Reasoning set)
	reasoning := "fallback-reasoning"
	d2 := &ChatCompletionsStreamResponseChoiceDelta{Reasoning: &reasoning}
	if d2.GetReasoningContent() != "fallback-reasoning" {
		t.Errorf("expected fallback to Reasoning field, got %q", d2.GetReasoningContent())
	}
}

func TestToolCallResponse_SetIndex(t *testing.T) {
	tc := &ToolCallResponse{}
	tc.SetIndex(3)
	if tc.Index == nil || *tc.Index != 3 {
		t.Errorf("Index = %v, want 3", tc.Index)
	}
}

func TestChatCompletionsStreamResponse_IsFinished(t *testing.T) {
	empty := &ChatCompletionsStreamResponse{}
	if empty.IsFinished() {
		t.Errorf("expected false with no choices")
	}

	noReason := &ChatCompletionsStreamResponse{Choices: []ChatCompletionsStreamResponseChoice{{}}}
	if noReason.IsFinished() {
		t.Errorf("expected false with nil FinishReason")
	}

	emptyReason := ""
	withEmptyReason := &ChatCompletionsStreamResponse{Choices: []ChatCompletionsStreamResponseChoice{{FinishReason: &emptyReason}}}
	if withEmptyReason.IsFinished() {
		t.Errorf("expected false with empty string FinishReason")
	}

	reason := "stop"
	finished := &ChatCompletionsStreamResponse{Choices: []ChatCompletionsStreamResponseChoice{{FinishReason: &reason}}}
	if !finished.IsFinished() {
		t.Errorf("expected true with non-empty FinishReason")
	}
}

func TestChatCompletionsStreamResponse_IsToolCall(t *testing.T) {
	empty := &ChatCompletionsStreamResponse{}
	if empty.IsToolCall() {
		t.Errorf("expected false with no choices")
	}

	noCalls := &ChatCompletionsStreamResponse{Choices: []ChatCompletionsStreamResponseChoice{{}}}
	if noCalls.IsToolCall() {
		t.Errorf("expected false with no tool calls")
	}

	withCalls := &ChatCompletionsStreamResponse{
		Choices: []ChatCompletionsStreamResponseChoice{
			{Delta: ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []ToolCallResponse{{ID: "id1"}}}},
		},
	}
	if !withCalls.IsToolCall() {
		t.Errorf("expected true with tool calls present")
	}
}

func TestChatCompletionsStreamResponse_GetFirstToolCall(t *testing.T) {
	empty := &ChatCompletionsStreamResponse{}
	if got := empty.GetFirstToolCall(); got != nil {
		t.Errorf("expected nil with no tool calls, got %+v", got)
	}

	withCalls := &ChatCompletionsStreamResponse{
		Choices: []ChatCompletionsStreamResponseChoice{
			{Delta: ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []ToolCallResponse{{ID: "id1"}, {ID: "id2"}}}},
		},
	}
	got := withCalls.GetFirstToolCall()
	if got == nil || got.ID != "id1" {
		t.Errorf("expected first tool call id1, got %+v", got)
	}
}

func TestChatCompletionsStreamResponse_ClearToolCalls(t *testing.T) {
	notoolcalls := &ChatCompletionsStreamResponse{Choices: []ChatCompletionsStreamResponseChoice{{}}}
	notoolcalls.ClearToolCalls() // no panic, no-op

	typeVal := "function"
	withCalls := &ChatCompletionsStreamResponse{
		Choices: []ChatCompletionsStreamResponseChoice{
			{Delta: ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []ToolCallResponse{
				{ID: "id1", Type: typeVal, Function: FunctionResponse{Name: "fn1"}},
			}}},
		},
	}
	withCalls.ClearToolCalls()
	tc := withCalls.Choices[0].Delta.ToolCalls[0]
	if tc.ID != "" || tc.Type != nil || tc.Function.Name != "" {
		t.Errorf("expected cleared tool call, got %+v", tc)
	}
}

func TestChatCompletionsStreamResponse_Copy(t *testing.T) {
	fp := "fp-1"
	orig := &ChatCompletionsStreamResponse{
		Id:                "resp-1",
		Object:            "chat.completion.chunk",
		Created:           123,
		Model:             "gpt-4",
		SystemFingerprint: &fp,
		Choices:           []ChatCompletionsStreamResponseChoice{{Index: 0}},
	}
	cp := orig.Copy()
	if cp == orig {
		t.Errorf("Copy() should return a different pointer")
	}
	if cp.Id != orig.Id || cp.Object != orig.Object || cp.Created != orig.Created || cp.Model != orig.Model {
		t.Errorf("Copy() field mismatch: %+v vs %+v", cp, orig)
	}
	if len(cp.Choices) != 1 || &cp.Choices[0] == &orig.Choices[0] {
		t.Errorf("Copy() should have independent Choices slice")
	}
}

func TestChatCompletionsStreamResponse_SystemFingerprint(t *testing.T) {
	c := &ChatCompletionsStreamResponse{}
	if c.GetSystemFingerprint() != "" {
		t.Errorf("expected empty fingerprint initially")
	}
	c.SetSystemFingerprint("fp-abc")
	if c.GetSystemFingerprint() != "fp-abc" {
		t.Errorf("GetSystemFingerprint() = %q, want fp-abc", c.GetSystemFingerprint())
	}
}

func TestOpenAIResponsesResponse_HasImageGenerationCall(t *testing.T) {
	empty := &OpenAIResponsesResponse{}
	if empty.HasImageGenerationCall() {
		t.Errorf("expected false for empty Output")
	}

	noMatch := &OpenAIResponsesResponse{Output: []ResponsesOutput{{Type: "message"}}}
	if noMatch.HasImageGenerationCall() {
		t.Errorf("expected false when no image_generation_call present")
	}

	withMatch := &OpenAIResponsesResponse{Output: []ResponsesOutput{
		{Type: "message"},
		{Type: ResponsesOutputTypeImageGenerationCall, Quality: "hd", Size: "1024x1024"},
	}}
	if !withMatch.HasImageGenerationCall() {
		t.Errorf("expected true when image_generation_call present")
	}
	if got := withMatch.GetQuality(); got != "hd" {
		t.Errorf("GetQuality() = %q, want hd", got)
	}
	if got := withMatch.GetSize(); got != "1024x1024" {
		t.Errorf("GetSize() = %q, want 1024x1024", got)
	}

	if got := empty.GetQuality(); got != "" {
		t.Errorf("GetQuality() on empty output = %q, want empty", got)
	}
	if got := empty.GetSize(); got != "" {
		t.Errorf("GetSize() on empty output = %q, want empty", got)
	}
	if got := noMatch.GetQuality(); got != "" {
		t.Errorf("GetQuality() with no match = %q, want empty", got)
	}
	if got := noMatch.GetSize(); got != "" {
		t.Errorf("GetSize() with no match = %q, want empty", got)
	}
}

// ---------- claude.go ----------

func TestClaudeMediaMessage_TextHelpers(t *testing.T) {
	m := &ClaudeMediaMessage{}
	if m.GetText() != "" {
		t.Errorf("expected empty text initially")
	}
	m.SetText("hello")
	if m.GetText() != "hello" {
		t.Errorf("GetText() = %q, want hello", m.GetText())
	}
}

func TestClaudeMediaMessage_ContentHelpers(t *testing.T) {
	m := &ClaudeMediaMessage{}
	if m.IsStringContent() {
		t.Errorf("expected false for nil content")
	}
	if m.GetStringContent() != "" {
		t.Errorf("expected empty string content for nil content")
	}

	m.SetContent("plain string")
	if !m.IsStringContent() {
		t.Errorf("expected true for string content")
	}
	if m.GetStringContent() != "plain string" {
		t.Errorf("GetStringContent() = %q, want 'plain string'", m.GetStringContent())
	}

	arrContent := []any{
		map[string]any{"type": "text", "text": "part1"},
		map[string]any{"type": "text", "text": "part2"},
		map[string]any{"type": "image"}, // ignored, not text
		"not a map",                     // ignored
	}
	m.SetContent(arrContent)
	if m.IsStringContent() {
		t.Errorf("expected false for array content")
	}
	if got := m.GetStringContent(); got != "part1part2" {
		t.Errorf("GetStringContent() = %q, want part1part2", got)
	}

	m.SetContent(42) // unsupported type
	if got := m.GetStringContent(); got != "" {
		t.Errorf("GetStringContent() with unsupported type = %q, want empty", got)
	}
}

func TestClaudeMediaMessage_GetJsonRowString(t *testing.T) {
	m := &ClaudeMediaMessage{Type: "text", Name: "n1"}
	s := m.GetJsonRowString()
	var out map[string]any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		t.Fatalf("expected valid json, got error: %v, raw=%s", err, s)
	}
	if out["type"] != "text" || out["name"] != "n1" {
		t.Errorf("unexpected json content: %+v", out)
	}
}

func TestClaudeMediaMessage_ParseMediaContent(t *testing.T) {
	m := &ClaudeMediaMessage{}
	if got := m.ParseMediaContent(); len(got) != 0 {
		t.Errorf("expected empty for nil content, got %+v", got)
	}
}

func TestClaudeMessage_ContentHelpers(t *testing.T) {
	m := &ClaudeMessage{}
	if m.IsStringContent() {
		t.Errorf("expected false for nil content")
	}
	if m.GetStringContent() != "" {
		t.Errorf("expected empty for nil content")
	}

	m.SetStringContent("hi")
	if !m.IsStringContent() {
		t.Errorf("expected true after SetStringContent")
	}
	if m.GetStringContent() != "hi" {
		t.Errorf("GetStringContent() = %q, want hi", m.GetStringContent())
	}

	arrContent := []any{
		map[string]any{"type": "text", "text": "x"},
		map[string]any{"type": "other"},
		"skip",
	}
	m.SetContent(arrContent)
	if got := m.GetStringContent(); got != "x" {
		t.Errorf("GetStringContent() = %q, want x", got)
	}

	m.SetContent(3.14)
	if got := m.GetStringContent(); got != "" {
		t.Errorf("GetStringContent() with unsupported type = %q, want empty", got)
	}
}

func TestThinking_GetBudgetTokens(t *testing.T) {
	th := &Thinking{}
	if th.GetBudgetTokens() != 0 {
		t.Errorf("expected 0 for nil BudgetTokens")
	}
	budget := 512
	th2 := &Thinking{BudgetTokens: &budget}
	if th2.GetBudgetTokens() != 512 {
		t.Errorf("GetBudgetTokens() = %d, want 512", th2.GetBudgetTokens())
	}
}

func TestClaudeRequest_SystemHelpers(t *testing.T) {
	c := &ClaudeRequest{}
	if c.IsStringSystem() {
		t.Errorf("expected false for nil System")
	}
	if c.GetStringSystem() != "" {
		t.Errorf("expected empty string system")
	}
	c.SetStringSystem("sys prompt")
	if !c.IsStringSystem() {
		t.Errorf("expected true after SetStringSystem")
	}
	if c.GetStringSystem() != "sys prompt" {
		t.Errorf("GetStringSystem() = %q, want 'sys prompt'", c.GetStringSystem())
	}

	c.System = []any{map[string]any{}}
	if c.IsStringSystem() {
		t.Errorf("expected false when System is non-string")
	}
	if c.GetStringSystem() != "" {
		t.Errorf("expected empty string when System is non-string")
	}
}

func TestClaudeRequest_SetModelName(t *testing.T) {
	c := &ClaudeRequest{Model: "orig"}
	c.SetModelName("")
	if c.Model != "orig" {
		t.Errorf("empty modelName should not overwrite")
	}
	c.SetModelName("claude-3")
	if c.Model != "claude-3" {
		t.Errorf("Model = %q, want claude-3", c.Model)
	}
}

func TestClaudeRequest_IsStream(t *testing.T) {
	c := &ClaudeRequest{Stream: true}
	if !c.IsStream(nil) {
		t.Errorf("expected true when Stream=true")
	}
	c2 := &ClaudeRequest{Stream: false}
	if c2.IsStream(nil) {
		t.Errorf("expected false when Stream=false")
	}
}

func TestClaudeRequest_SearchToolNameByToolCallId(t *testing.T) {
	c := &ClaudeRequest{
		Messages: []ClaudeMessage{
			{Content: []any{
				map[string]any{"type": "tool_use", "id": "call-1", "name": "search"},
			}},
		},
	}
	if got := c.SearchToolNameByToolCallId("call-1"); got != "search" {
		t.Errorf("SearchToolNameByToolCallId() = %q, want search", got)
	}
	if got := c.SearchToolNameByToolCallId("missing"); got != "" {
		t.Errorf("SearchToolNameByToolCallId() for missing id = %q, want empty", got)
	}
}

func TestClaudeRequest_AddToolGetTools(t *testing.T) {
	c := &ClaudeRequest{}
	if got := c.GetTools(); got != nil {
		t.Errorf("expected nil tools initially, got %+v", got)
	}

	c.AddTool(Tool{Name: "t1"})
	tools := c.GetTools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool after AddTool, got %d", len(tools))
	}

	c.AddTool(Tool{Name: "t2"})
	tools2 := c.GetTools()
	if len(tools2) != 2 {
		t.Fatalf("expected 2 tools after second AddTool, got %d", len(tools2))
	}

	// Tools field set to non-[]any type -> AddTool reinitializes
	c2 := &ClaudeRequest{Tools: "not a slice"}
	c2.AddTool(Tool{Name: "t3"})
	tools3, ok := c2.Tools.([]any)
	if !ok || len(tools3) != 1 {
		t.Errorf("expected Tools reinitialized to []any with 1 item, got %+v", c2.Tools)
	}

	// GetTools with non-[]any type returns nil
	c3 := &ClaudeRequest{Tools: "not a slice"}
	if got := c3.GetTools(); got != nil {
		t.Errorf("expected nil for non-slice Tools, got %+v", got)
	}
}

func TestProcessTools(t *testing.T) {
	tool := &Tool{Name: "normal"}
	toolVal := Tool{Name: "normal-val"}
	webTool := &ClaudeWebSearchTool{Name: "web"}
	webToolVal := ClaudeWebSearchTool{Name: "web-val"}

	normal, web := ProcessTools([]any{tool, toolVal, webTool, webToolVal, "unknown"})
	if len(normal) != 2 {
		t.Fatalf("expected 2 normal tools, got %d", len(normal))
	}
	if len(web) != 2 {
		t.Fatalf("expected 2 web search tools, got %d", len(web))
	}
	if normal[0].Name != "normal" || normal[1].Name != "normal-val" {
		t.Errorf("normal tools mismatch: %+v", normal)
	}
	if web[0].Name != "web" || web[1].Name != "web-val" {
		t.Errorf("web tools mismatch: %+v", web)
	}

	emptyNormal, emptyWeb := ProcessTools(nil)
	if emptyNormal != nil || emptyWeb != nil {
		t.Errorf("expected nil slices for nil input, got %+v %+v", emptyNormal, emptyWeb)
	}
}

func TestClaudeResponse_Index(t *testing.T) {
	c := &ClaudeResponse{}
	if c.GetIndex() != 0 {
		t.Errorf("expected 0 for nil Index")
	}
	c.SetIndex(5)
	if c.GetIndex() != 5 {
		t.Errorf("GetIndex() = %d, want 5", c.GetIndex())
	}
}

func TestClaudeResponse_GetClaudeError(t *testing.T) {
	empty := &ClaudeResponse{}
	if got := empty.GetClaudeError(); got != nil {
		t.Errorf("expected nil for nil Error, got %+v", got)
	}

	valErr := &ClaudeResponse{Error: types.ClaudeError{Type: "t", Message: "m"}}
	got := valErr.GetClaudeError()
	if got == nil || got.Type != "t" || got.Message != "m" {
		t.Errorf("value case mismatch: %+v", got)
	}

	ptrErr := &types.ClaudeError{Type: "pt", Message: "pm"}
	ptrCase := &ClaudeResponse{Error: ptrErr}
	if got := ptrCase.GetClaudeError(); got != ptrErr {
		t.Errorf("pointer case should return same pointer")
	}

	mapCase := &ClaudeResponse{Error: map[string]interface{}{"type": "map_t", "message": "map_m"}}
	gotMap := mapCase.GetClaudeError()
	if gotMap == nil || gotMap.Type != "map_t" || gotMap.Message != "map_m" {
		t.Errorf("map case mismatch: %+v", gotMap)
	}

	strCase := &ClaudeResponse{Error: "string error"}
	gotStr := strCase.GetClaudeError()
	if gotStr == nil || gotStr.Type != "upstream_error" || gotStr.Message != "string error" {
		t.Errorf("string case mismatch: %+v", gotStr)
	}

	defaultCase := &ClaudeResponse{Error: 999}
	gotDefault := defaultCase.GetClaudeError()
	if gotDefault == nil || gotDefault.Type != "unknown_upstream_error" {
		t.Errorf("default case mismatch: %+v", gotDefault)
	}
}

func TestClaudeUsage_CacheHelpers(t *testing.T) {
	var nilUsage *ClaudeUsage
	if nilUsage.GetCacheCreation5mTokens() != 0 {
		t.Errorf("nil receiver GetCacheCreation5mTokens should be 0")
	}
	if nilUsage.GetCacheCreation1hTokens() != 0 {
		t.Errorf("nil receiver GetCacheCreation1hTokens should be 0")
	}
	if nilUsage.GetCacheCreationTotalTokens() != 0 {
		t.Errorf("nil receiver GetCacheCreationTotalTokens should be 0")
	}

	noCacheCreation := &ClaudeUsage{}
	if noCacheCreation.GetCacheCreation5mTokens() != 0 {
		t.Errorf("expected 0 when CacheCreation nil")
	}
	if noCacheCreation.GetCacheCreation1hTokens() != 0 {
		t.Errorf("expected 0 when CacheCreation nil")
	}

	withCacheCreation := &ClaudeUsage{
		CacheCreation: &ClaudeCacheCreationUsage{Ephemeral5mInputTokens: 10, Ephemeral1hInputTokens: 20},
	}
	if withCacheCreation.GetCacheCreation5mTokens() != 10 {
		t.Errorf("GetCacheCreation5mTokens() = %d, want 10", withCacheCreation.GetCacheCreation5mTokens())
	}
	if withCacheCreation.GetCacheCreation1hTokens() != 20 {
		t.Errorf("GetCacheCreation1hTokens() = %d, want 20", withCacheCreation.GetCacheCreation1hTokens())
	}
	if withCacheCreation.GetCacheCreationTotalTokens() != 30 {
		t.Errorf("GetCacheCreationTotalTokens() = %d, want 30 (fallback sum)", withCacheCreation.GetCacheCreationTotalTokens())
	}

	withDirectTotal := &ClaudeUsage{CacheCreationInputTokens: 99, CacheCreation: &ClaudeCacheCreationUsage{Ephemeral5mInputTokens: 10}}
	if withDirectTotal.GetCacheCreationTotalTokens() != 99 {
		t.Errorf("expected direct CacheCreationInputTokens (99) to take priority, got %d", withDirectTotal.GetCacheCreationTotalTokens())
	}
}

// ---------- openai_request.go ----------

func TestGeneralOpenAIRequest_ParseInput(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  []string
	}{
		{name: "nil input", input: nil, want: nil},
		{name: "string input", input: "solo", want: []string{"solo"}},
		{name: "array input", input: []any{"x", "y"}, want: []string{"x", "y"}},
		{name: "array with non-string filtered", input: []any{"x", 5}, want: []string{"x"}},
		{name: "unsupported type", input: 7, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &GeneralOpenAIRequest{Input: tt.input}
			got := r.ParseInput()
			if len(got) != len(tt.want) {
				t.Fatalf("ParseInput() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ParseInput()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestGeneralOpenAIRequest_IsStream(t *testing.T) {
	r := &GeneralOpenAIRequest{Stream: true}
	if !r.IsStream(nil) {
		t.Errorf("expected true when Stream=true")
	}
	r2 := &GeneralOpenAIRequest{Stream: false}
	if r2.IsStream(nil) {
		t.Errorf("expected false when Stream=false")
	}
}

func TestGeneralOpenAIRequest_SetModelName(t *testing.T) {
	r := &GeneralOpenAIRequest{Model: "orig"}
	r.SetModelName("")
	if r.Model != "orig" {
		t.Errorf("empty modelName should not overwrite")
	}
	r.SetModelName("gpt-4o")
	if r.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o", r.Model)
	}
}

func TestGeneralOpenAIRequest_ToMap(t *testing.T) {
	r := &GeneralOpenAIRequest{Model: "gpt-4o", Stream: true}
	m := r.ToMap()
	if m["model"] != "gpt-4o" {
		t.Errorf("ToMap()[model] = %v, want gpt-4o", m["model"])
	}
	if m["stream"] != true {
		t.Errorf("ToMap()[stream] = %v, want true", m["stream"])
	}
}

func TestGeneralOpenAIRequest_GetSystemRoleName(t *testing.T) {
	tests := []struct {
		model string
		want  string
	}{
		{model: "o1", want: "developer"},
		{model: "o3-mini", want: "developer"},
		{model: "o1-mini", want: "system"},
		{model: "o1-preview", want: "system"},
		{model: "gpt-5", want: "developer"},
		{model: "gpt-5-mini", want: "developer"},
		{model: "gpt-4o", want: "system"},
		{model: "claude-3", want: "system"},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			r := &GeneralOpenAIRequest{Model: tt.model}
			if got := r.GetSystemRoleName(); got != tt.want {
				t.Errorf("GetSystemRoleName() for model %q = %q, want %q", tt.model, got, tt.want)
			}
		})
	}
}

func TestGeneralOpenAIRequest_GetMaxTokens(t *testing.T) {
	r := &GeneralOpenAIRequest{MaxTokens: 100}
	if r.GetMaxTokens() != 100 {
		t.Errorf("GetMaxTokens() = %d, want 100", r.GetMaxTokens())
	}
	r2 := &GeneralOpenAIRequest{MaxTokens: 100, MaxCompletionTokens: 200}
	if r2.GetMaxTokens() != 200 {
		t.Errorf("GetMaxTokens() = %d, want 200 (MaxCompletionTokens priority)", r2.GetMaxTokens())
	}
}

func TestGeneralOpenAIRequest_GetTokenCountMeta(t *testing.T) {
	name := "func1"
	r := &GeneralOpenAIRequest{
		Prompt:              "prompt-text",
		MaxTokens:           50,
		MaxCompletionTokens: 100,
		Messages: []Message{
			{Role: "user", Content: "hello", Name: &name},
		},
		Tools: []ToolCallRequest{
			{Function: FunctionRequest{Name: "fn1", Description: "desc1", Parameters: map[string]any{"a": 1}}},
		},
	}
	meta := r.GetTokenCountMeta()
	if meta.MaxTokens != 100 {
		t.Errorf("MaxTokens = %d, want 100 (MaxCompletionTokens > MaxTokens)", meta.MaxTokens)
	}
	if meta.MessagesCount != 1 {
		t.Errorf("MessagesCount = %d, want 1", meta.MessagesCount)
	}
	if meta.NameCount != 1 {
		t.Errorf("NameCount = %d, want 1", meta.NameCount)
	}
	if meta.ToolsCount != 1 {
		t.Errorf("ToolsCount = %d, want 1", meta.ToolsCount)
	}

	// Prompt as []any
	r2 := &GeneralOpenAIRequest{Prompt: []any{"p1", "p2", 3}}
	meta2 := r2.GetTokenCountMeta()
	if meta2.CombineText != "p1\np2" {
		t.Errorf("CombineText = %q, want p1\\np2", meta2.CombineText)
	}

	// Prompt as unsupported type -> fmt.Sprintf branch
	r3 := &GeneralOpenAIRequest{Prompt: 12}
	meta3 := r3.GetTokenCountMeta()
	if meta3.CombineText != "12" {
		t.Errorf("CombineText = %q, want 12", meta3.CombineText)
	}

	// MaxTokens > MaxCompletionTokens branch
	r4 := &GeneralOpenAIRequest{MaxTokens: 300, MaxCompletionTokens: 10}
	meta4 := r4.GetTokenCountMeta()
	if meta4.MaxTokens != 300 {
		t.Errorf("MaxTokens = %d, want 300", meta4.MaxTokens)
	}

	// Message with media content: image_url, input_audio, file, video_url, and generic text
	r5 := &GeneralOpenAIRequest{
		Messages: []Message{
			{Role: "user", Content: []any{
				map[string]any{"type": "image_url", "image_url": map[string]interface{}{"url": "http://img", "detail": "high"}},
				map[string]any{"type": "input_audio", "input_audio": map[string]interface{}{"data": "audiodata", "format": "mp3"}},
				map[string]any{"type": "file", "file": map[string]interface{}{"file_id": "fid1"}},
				map[string]any{"type": "video_url", "video_url": "http://vid"},
				map[string]any{"type": "text", "text": "hello text"},
			}},
		},
	}
	meta5 := r5.GetTokenCountMeta()
	if len(meta5.Files) != 4 {
		t.Fatalf("expected 4 files, got %d: %+v", len(meta5.Files), meta5.Files)
	}
	wantTypes := []types.FileType{types.FileTypeImage, types.FileTypeAudio, types.FileTypeFile, types.FileTypeVideo}
	for i, f := range meta5.Files {
		if f.FileType != wantTypes[i] {
			t.Errorf("Files[%d].FileType = %v, want %v", i, f.FileType, wantTypes[i])
		}
	}
}

func TestMediaContent_Getters(t *testing.T) {
	// nil cases
	mc := &MediaContent{}
	if mc.GetImageMedia() != nil {
		t.Errorf("expected nil GetImageMedia for nil ImageUrl")
	}
	if mc.GetInputAudio() != nil {
		t.Errorf("expected nil GetInputAudio for nil InputAudio")
	}
	if mc.GetFile() != nil {
		t.Errorf("expected nil GetFile for nil File")
	}
	if mc.GetVideoUrl() != nil {
		t.Errorf("expected nil GetVideoUrl for nil VideoUrl")
	}

	// pointer-typed fields
	imgPtr := &MessageImageUrl{Url: "http://x"}
	mc2 := &MediaContent{ImageUrl: imgPtr}
	if got := mc2.GetImageMedia(); got != imgPtr {
		t.Errorf("expected same pointer returned")
	}

	audioPtr := &MessageInputAudio{Data: "d"}
	mc3 := &MediaContent{InputAudio: audioPtr}
	if got := mc3.GetInputAudio(); got != audioPtr {
		t.Errorf("expected same pointer returned")
	}

	filePtr := &MessageFile{FileId: "f1"}
	mc4 := &MediaContent{File: filePtr}
	if got := mc4.GetFile(); got != filePtr {
		t.Errorf("expected same pointer returned")
	}

	videoPtr := &MessageVideoUrl{Url: "http://v"}
	mc5 := &MediaContent{VideoUrl: videoPtr}
	if got := mc5.GetVideoUrl(); got != videoPtr {
		t.Errorf("expected same pointer returned")
	}

	// map-typed fields
	mc6 := &MediaContent{ImageUrl: map[string]any{"url": "u1", "detail": "low", "mime_type": "image/png"}}
	got6 := mc6.GetImageMedia()
	if got6 == nil || got6.Url != "u1" || got6.Detail != "low" || got6.MimeType != "image/png" {
		t.Errorf("map ImageUrl mismatch: %+v", got6)
	}

	mc7 := &MediaContent{InputAudio: map[string]any{"data": "d1", "format": "wav"}}
	got7 := mc7.GetInputAudio()
	if got7 == nil || got7.Data != "d1" || got7.Format != "wav" {
		t.Errorf("map InputAudio mismatch: %+v", got7)
	}

	mc8 := &MediaContent{File: map[string]any{"file_name": "n1", "file_data": "d1", "file_id": "id1"}}
	got8 := mc8.GetFile()
	if got8 == nil || got8.FileName != "n1" || got8.FileData != "d1" || got8.FileId != "id1" {
		t.Errorf("map File mismatch: %+v", got8)
	}

	mc9 := &MediaContent{VideoUrl: map[string]any{"url": "u2"}}
	got9 := mc9.GetVideoUrl()
	if got9 == nil || got9.Url != "u2" {
		t.Errorf("map VideoUrl mismatch: %+v", got9)
	}
}

func TestMessageImageUrl_IsRemoteImage(t *testing.T) {
	remote := &MessageImageUrl{Url: "http://example.com/x.png"}
	if !remote.IsRemoteImage() {
		t.Errorf("expected true for http prefix")
	}
	local := &MessageImageUrl{Url: "data:image/png;base64,abc"}
	if local.IsRemoteImage() {
		t.Errorf("expected false for non-http prefix")
	}
}

func TestMessage_PrefixHelpers(t *testing.T) {
	m := &Message{}
	if m.GetPrefix() {
		t.Errorf("expected false for nil Prefix")
	}
	m.SetPrefix(true)
	if !m.GetPrefix() {
		t.Errorf("expected true after SetPrefix(true)")
	}
	m.SetPrefix(false)
	if m.GetPrefix() {
		t.Errorf("expected false after SetPrefix(false)")
	}
}

func TestMessage_ToolCallsHelpers(t *testing.T) {
	m := &Message{}
	if got := m.ParseToolCalls(); got != nil {
		t.Errorf("expected nil for nil ToolCalls, got %+v", got)
	}

	m.SetToolCalls([]ToolCallRequest{{ID: "id1", Type: "function", Function: FunctionRequest{Name: "fn"}}})
	parsed := m.ParseToolCalls()
	if len(parsed) != 1 || parsed[0].ID != "id1" || parsed[0].Function.Name != "fn" {
		t.Errorf("ParseToolCalls() mismatch: %+v", parsed)
	}

	// malformed json -> returns nil (err path, empty toolCalls var returned)
	m2 := &Message{ToolCalls: json.RawMessage(`not json`)}
	if got := m2.ParseToolCalls(); got != nil {
		t.Errorf("expected nil for malformed ToolCalls json, got %+v", got)
	}
}

func TestMessage_StringContent(t *testing.T) {
	m := &Message{Content: "plain"}
	if got := m.StringContent(); got != "plain" {
		t.Errorf("StringContent() = %q, want plain", got)
	}

	m2 := &Message{Content: []any{
		map[string]any{"type": "text", "text": "a"},
		map[string]any{"type": "text", "text": "b"},
		map[string]any{"type": "other"},
		"skip",
	}}
	if got := m2.StringContent(); got != "ab" {
		t.Errorf("StringContent() = %q, want ab", got)
	}

	m3 := &Message{Content: 5}
	if got := m3.StringContent(); got != "" {
		t.Errorf("StringContent() unsupported type = %q, want empty", got)
	}
}

func TestMessage_ContentSetters(t *testing.T) {
	m := &Message{}
	m.SetStringContent("hi")
	if !m.IsStringContent() || m.Content != "hi" {
		t.Errorf("SetStringContent mismatch: %+v", m)
	}

	m.SetMediaContent([]MediaContent{{Type: ContentTypeText, Text: "x"}})
	if m.IsStringContent() {
		t.Errorf("expected false after SetMediaContent")
	}

	m.SetNullContent()
	if m.Content != nil {
		t.Errorf("expected nil Content after SetNullContent")
	}
	if got := m.ParseContent(); got != nil {
		t.Errorf("expected nil ParseContent after SetNullContent, got %+v", got)
	}
}

func TestMessage_ParseContent(t *testing.T) {
	// string content
	m := &Message{Content: "just text"}
	parsed := m.ParseContent()
	if len(parsed) != 1 || parsed[0].Type != ContentTypeText || parsed[0].Text != "just text" {
		t.Fatalf("string content parse mismatch: %+v", parsed)
	}
	// cached: second call returns cached parsedContent
	parsed2 := m.ParseContent()
	if len(parsed2) != 1 {
		t.Fatalf("expected cached parse result")
	}

	// nil content
	mNil := &Message{}
	if got := mNil.ParseContent(); got != nil {
		t.Errorf("expected nil for nil content, got %+v", got)
	}

	// non-string, non-array content -> nil contentList
	mOther := &Message{Content: 42}
	if got := mOther.ParseContent(); got != nil {
		t.Errorf("expected nil for unsupported content type, got %+v", got)
	}

	// already-typed MediaContent items in array
	mTyped := &Message{Content: []any{MediaContent{Type: ContentTypeText, Text: "typed"}}}
	parsedTyped := mTyped.ParseContent()
	if len(parsedTyped) != 1 || parsedTyped[0].Text != "typed" {
		t.Fatalf("typed MediaContent parse mismatch: %+v", parsedTyped)
	}

	// array item missing type field
	mMissingType := &Message{Content: []any{map[string]any{"text": "no type"}}}
	if got := mMissingType.ParseContent(); len(got) != 0 {
		t.Errorf("expected empty result for missing type field, got %+v", got)
	}

	// image_url as string form
	mImgStr := &Message{Content: []any{
		map[string]any{"type": ContentTypeImageURL, "image_url": "http://simple"},
	}}
	parsedImgStr := mImgStr.ParseContent()
	if len(parsedImgStr) != 1 {
		t.Fatalf("expected 1 parsed image item, got %+v", parsedImgStr)
	}
	imgUrl, ok := parsedImgStr[0].ImageUrl.(*MessageImageUrl)
	if !ok || imgUrl.Url != "http://simple" || imgUrl.Detail != "high" {
		t.Errorf("image_url string form mismatch: %+v", parsedImgStr[0].ImageUrl)
	}

	// image_url as object form with explicit detail
	mImgObj := &Message{Content: []any{
		map[string]any{"type": ContentTypeImageURL, "image_url": map[string]interface{}{"url": "http://obj", "detail": "low"}},
	}}
	parsedImgObj := mImgObj.ParseContent()
	imgUrl2 := parsedImgObj[0].ImageUrl.(*MessageImageUrl)
	if imgUrl2.Url != "http://obj" || imgUrl2.Detail != "low" {
		t.Errorf("image_url object form mismatch: %+v", imgUrl2)
	}

	// input_audio missing required subfields -> skipped
	mAudioBad := &Message{Content: []any{
		map[string]any{"type": ContentTypeInputAudio, "input_audio": map[string]interface{}{"data": "d"}}, // missing format
	}}
	if got := mAudioBad.ParseContent(); len(got) != 0 {
		t.Errorf("expected empty result for incomplete input_audio, got %+v", got)
	}

	// file with filename+file_data (no file_id)
	mFile := &Message{Content: []any{
		map[string]any{"type": ContentTypeFile, "file": map[string]interface{}{"filename": "f.txt", "file_data": "base64data"}},
	}}
	parsedFile := mFile.ParseContent()
	if len(parsedFile) != 1 {
		t.Fatalf("expected 1 parsed file item, got %+v", parsedFile)
	}
	fileVal := parsedFile[0].File.(*MessageFile)
	if fileVal.FileName != "f.txt" || fileVal.FileData != "base64data" {
		t.Errorf("file parse mismatch: %+v", fileVal)
	}

	// video_url
	mVideo := &Message{Content: []any{
		map[string]any{"type": ContentTypeVideoUrl, "video_url": "http://v1"},
	}}
	parsedVideo := mVideo.ParseContent()
	if len(parsedVideo) != 1 {
		t.Fatalf("expected 1 parsed video item, got %+v", parsedVideo)
	}
	videoVal := parsedVideo[0].VideoUrl.(*MessageVideoUrl)
	if videoVal.Url != "http://v1" {
		t.Errorf("video_url parse mismatch: %+v", videoVal)
	}

	// array item not a map -> skipped
	mNotMap := &Message{Content: []any{42, "skip me"}}
	if got := mNotMap.ParseContent(); len(got) != 0 {
		t.Errorf("expected empty for non-map array items, got %+v", got)
	}
}

// ---------- openai_response.go (OpenAIResponsesRequest) ----------

func TestOpenAIResponsesRequest_IsStream(t *testing.T) {
	r := &OpenAIResponsesRequest{Stream: true}
	if !r.IsStream(nil) {
		t.Errorf("expected true when Stream=true")
	}
}

func TestOpenAIResponsesRequest_SetModelName(t *testing.T) {
	r := &OpenAIResponsesRequest{Model: "orig"}
	r.SetModelName("")
	if r.Model != "orig" {
		t.Errorf("empty modelName should not overwrite")
	}
	r.SetModelName("gpt-5")
	if r.Model != "gpt-5" {
		t.Errorf("Model = %q, want gpt-5", r.Model)
	}
}

func TestOpenAIResponsesRequest_GetToolsMap(t *testing.T) {
	r := &OpenAIResponsesRequest{}
	if got := r.GetToolsMap(); got != nil {
		t.Errorf("expected nil for empty Tools, got %+v", got)
	}

	r2 := &OpenAIResponsesRequest{Tools: json.RawMessage(`[{"type":"function","name":"fn1"}]`)}
	got2 := r2.GetToolsMap()
	if len(got2) != 1 || got2[0]["name"] != "fn1" {
		t.Errorf("GetToolsMap() mismatch: %+v", got2)
	}
}

func TestOpenAIResponsesRequest_GetTokenCountMeta(t *testing.T) {
	r := &OpenAIResponsesRequest{
		Input:           json.RawMessage(`"simple string input"`),
		Instructions:    json.RawMessage(`"instr"`),
		Metadata:        json.RawMessage(`{"k":"v"}`),
		Text:            json.RawMessage(`{"format":"json"}`),
		ToolChoice:      json.RawMessage(`"auto"`),
		Prompt:          json.RawMessage(`"prompt text"`),
		Tools:           json.RawMessage(`[{"type":"function"}]`),
		MaxOutputTokens: 500,
	}
	meta := r.GetTokenCountMeta()
	if meta.MaxTokens != 500 {
		t.Errorf("MaxTokens = %d, want 500", meta.MaxTokens)
	}
	wantText := "simple string input\n\"instr\"\n{\"k\":\"v\"}\n{\"format\":\"json\"}\n\"auto\"\n\"prompt text\"\n[{\"type\":\"function\"}]"
	if meta.CombineText != wantText {
		t.Errorf("CombineText = %q, want %q", meta.CombineText, wantText)
	}

	// array of Input with image and file types
	r2 := &OpenAIResponsesRequest{
		Input: json.RawMessage(`[
			{"type":"message","role":"user","content":[
				{"type":"input_image","image_url":"http://img"},
				{"type":"input_file","file_url":"http://file"},
				{"type":"input_text","text":"hi there"}
			]}
		]`),
	}
	meta2 := r2.GetTokenCountMeta()
	if len(meta2.Files) != 2 {
		t.Fatalf("expected 2 files, got %d: %+v", len(meta2.Files), meta2.Files)
	}
	if meta2.Files[0].FileType != types.FileTypeImage || meta2.Files[0].OriginData != "http://img" {
		t.Errorf("first file mismatch: %+v", meta2.Files[0])
	}
	if meta2.Files[1].FileType != types.FileTypeFile || meta2.Files[1].OriginData != "http://file" {
		t.Errorf("second file mismatch: %+v", meta2.Files[1])
	}
	if meta2.CombineText != "hi there" {
		t.Errorf("CombineText = %q, want 'hi there'", meta2.CombineText)
	}

	// image/file as object with url field
	r3 := &OpenAIResponsesRequest{
		Input: json.RawMessage(`[
			{"type":"message","content":[
				{"type":"input_image","image_url":{"url":"http://img2"}},
				{"type":"input_file","file_url":{"url":"http://file2"}}
			]}
		]`),
	}
	meta3 := r3.GetTokenCountMeta()
	if len(meta3.Files) != 2 || meta3.Files[0].OriginData != "http://img2" || meta3.Files[1].OriginData != "http://file2" {
		t.Errorf("object url form mismatch: %+v", meta3.Files)
	}

	// nil Input
	r4 := &OpenAIResponsesRequest{}
	meta4 := r4.GetTokenCountMeta()
	if meta4.CombineText != "" {
		t.Errorf("expected empty CombineText for nil Input, got %q", meta4.CombineText)
	}
}

func TestOpenAIResponsesRequest_ParseInput(t *testing.T) {
	r := &OpenAIResponsesRequest{}
	if got := r.ParseInput(); got != nil {
		t.Errorf("expected nil for nil Input, got %+v", got)
	}

	r2 := &OpenAIResponsesRequest{Input: json.RawMessage(`"str input"`)}
	got2 := r2.ParseInput()
	if len(got2) != 1 || got2[0].Type != "input_text" || got2[0].Text != "str input" {
		t.Errorf("string input parse mismatch: %+v", got2)
	}

	// unsupported json type (number) -> empty
	r3 := &OpenAIResponsesRequest{Input: json.RawMessage(`42`)}
	if got := r3.ParseInput(); len(got) != 0 {
		t.Errorf("expected empty for number input, got %+v", got)
	}
}

// ---------- openai_image.go ----------

func TestImageRequest_MarshalUnmarshalJSON(t *testing.T) {
	raw := []byte(`{"model":"dall-e-3","prompt":"a cat","n":2,"size":"1024x1024","extra_unknown_field":"xyz"}`)
	var r ImageRequest
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if r.Model != "dall-e-3" || r.Prompt != "a cat" || r.N != 2 || r.Size != "1024x1024" {
		t.Errorf("known field parse mismatch: %+v", r)
	}
	if string(r.Extra["extra_unknown_field"]) != `"xyz"` {
		t.Errorf("Extra field mismatch: %+v", r.Extra)
	}

	out, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var roundTrip map[string]any
	if err := json.Unmarshal(out, &roundTrip); err != nil {
		t.Fatalf("roundtrip unmarshal error: %v", err)
	}
	if roundTrip["model"] != "dall-e-3" || roundTrip["prompt"] != "a cat" {
		t.Errorf("roundtrip mismatch: %+v", roundTrip)
	}
	// Extra fields must NOT be merged into marshaled output (per source comment).
	if _, exists := roundTrip["extra_unknown_field"]; exists {
		t.Errorf("did not expect extra_unknown_field in marshaled output: %+v", roundTrip)
	}

	// malformed json -> error
	var bad ImageRequest
	if err := json.Unmarshal([]byte(`not json`), &bad); err == nil {
		t.Errorf("expected error for malformed json")
	}
}

func TestGetJSONFieldNames(t *testing.T) {
	names := GetJSONFieldNames(reflect.TypeOf(ImageRequest{}))
	if _, ok := names["model"]; !ok {
		t.Errorf("expected 'model' field name present: %+v", names)
	}
	if _, ok := names["prompt"]; !ok {
		t.Errorf("expected 'prompt' field name present: %+v", names)
	}
	// Extra field has json:"-" and should be excluded
	if _, ok := names["-"]; ok {
		t.Errorf("did not expect '-' field name present: %+v", names)
	}
}

func TestImageRequest_GetTokenCountMeta(t *testing.T) {
	tests := []struct {
		name        string
		req         ImageRequest
		wantRatio   float64
		wantCombine string
	}{
		{
			name:        "non-dalle model default ratio",
			req:         ImageRequest{Model: "other-model", Prompt: "p", N: 1},
			wantRatio:   1.0,
			wantCombine: "p",
		},
		{
			name:      "dall-e 256x256",
			req:       ImageRequest{Model: "dall-e-2", Size: "256x256", N: 1},
			wantRatio: 0.4,
		},
		{
			name:      "dall-e 512x512",
			req:       ImageRequest{Model: "dall-e-2", Size: "512x512", N: 1},
			wantRatio: 0.45,
		},
		{
			name:      "dall-e 1024x1024",
			req:       ImageRequest{Model: "dall-e-2", Size: "1024x1024", N: 1},
			wantRatio: 1,
		},
		{
			name:      "dall-e 1024x1792",
			req:       ImageRequest{Model: "dall-e-2", Size: "1024x1792", N: 1},
			wantRatio: 2,
		},
		{
			name:      "dall-e-3 hd quality standard size",
			req:       ImageRequest{Model: "dall-e-3", Size: "1024x1024", Quality: "hd", N: 1},
			wantRatio: 2.0,
		},
		{
			name:      "dall-e-3 hd quality wide size",
			req:       ImageRequest{Model: "dall-e-3", Size: "1024x1792", Quality: "hd", N: 1},
			wantRatio: 3.0, // sizeRatio(2) * qualityRatio(1.5)
		},
		{
			name:      "N multiplier applied",
			req:       ImageRequest{Model: "dall-e-2", Size: "1024x1024", N: 3},
			wantRatio: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := tt.req.GetTokenCountMeta()
			if meta.ImagePriceRatio != tt.wantRatio {
				t.Errorf("ImagePriceRatio = %v, want %v", meta.ImagePriceRatio, tt.wantRatio)
			}
			if meta.MaxTokens != 1584 {
				t.Errorf("MaxTokens = %d, want 1584", meta.MaxTokens)
			}
		})
	}
}

func TestImageRequest_IsStream(t *testing.T) {
	r := &ImageRequest{}
	if r.IsStream(nil) {
		t.Errorf("ImageRequest.IsStream should always be false")
	}
}

func TestImageRequest_SetModelName(t *testing.T) {
	r := &ImageRequest{Model: "orig"}
	r.SetModelName("")
	if r.Model != "orig" {
		t.Errorf("empty modelName should not overwrite")
	}
	r.SetModelName("dall-e-3")
	if r.Model != "dall-e-3" {
		t.Errorf("Model = %q, want dall-e-3", r.Model)
	}
}

// ---------- additional gap coverage ----------

func TestClaudeRequest_ParseSystem(t *testing.T) {
	c := &ClaudeRequest{}
	if got := c.ParseSystem(); len(got) != 0 {
		t.Errorf("expected empty for nil System, got %+v", got)
	}

	c.System = []any{
		map[string]any{"type": "text", "text": "sys text"},
	}
	got := c.ParseSystem()
	if len(got) != 1 || got[0].Type != "text" || got[0].GetText() != "sys text" {
		t.Errorf("ParseSystem() mismatch: %+v", got)
	}
}

func TestClaudeRequest_GetTokenCountMeta(t *testing.T) {
	name := "webtool"
	c := &ClaudeRequest{
		MaxTokens: 128,
		System:    "system prompt",
		Messages: []ClaudeMessage{
			{Role: "user", Content: "hello there"},
			{Role: "assistant", Content: []any{
				map[string]any{"type": "text", "text": "reply text"},
				map[string]any{"type": "image", "source": map[string]any{"type": "base64", "data": "imgdata"}},
				map[string]any{"type": "tool_use", "name": "calc", "input": map[string]any{"a": 1}},
				map[string]any{"type": "tool_result", "content": "tool output"},
			}},
		},
		Tools: []any{
			Tool{Name: "calc", Description: "does math", InputSchema: map[string]interface{}{"type": "object"}},
			ClaudeWebSearchTool{Name: name},
		},
	}
	meta := c.GetTokenCountMeta()
	if meta.MaxTokens != 128 {
		t.Errorf("MaxTokens = %d, want 128", meta.MaxTokens)
	}
	if meta.MessagesCount != 2 {
		t.Errorf("MessagesCount = %d, want 2", meta.MessagesCount)
	}
	if meta.ToolsCount != 2 {
		t.Errorf("ToolsCount = %d, want 2", meta.ToolsCount)
	}
	if len(meta.Files) != 1 || meta.Files[0].FileType != types.FileTypeImage || meta.Files[0].OriginData != "imgdata" {
		t.Errorf("Files mismatch: %+v", meta.Files)
	}

	// system as media array (image + text)
	c2 := &ClaudeRequest{
		System: []any{
			map[string]any{"type": "text", "text": "sys media text"},
			map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": "http://sysimg"}},
		},
	}
	meta2 := c2.GetTokenCountMeta()
	if len(meta2.Files) != 1 || meta2.Files[0].OriginData != "http://sysimg" {
		t.Errorf("system media files mismatch: %+v", meta2.Files)
	}

	// empty message content string is not appended
	c3 := &ClaudeRequest{Messages: []ClaudeMessage{{Role: "user", Content: ""}}}
	meta3 := c3.GetTokenCountMeta()
	if meta3.CombineText != "user" {
		t.Errorf("CombineText = %q, want user", meta3.CombineText)
	}
}

func TestOpenAIResponsesResponse_GetOpenAIError(t *testing.T) {
	o := &OpenAIResponsesResponse{Error: map[string]interface{}{"message": "resp-err"}}
	got := o.GetOpenAIError()
	if got == nil || got.Message != "resp-err" {
		t.Errorf("GetOpenAIError() mismatch: %+v", got)
	}

	o2 := &OpenAIResponsesResponse{}
	if got := o2.GetOpenAIError(); got != nil {
		t.Errorf("expected nil for unset Error, got %+v", got)
	}
}

func TestTaskResponse_IsSuccess(t *testing.T) {
	success := &TaskResponse[string]{Code: TaskSuccessCode}
	if !success.IsSuccess() {
		t.Errorf("expected true for success code")
	}
	failed := &TaskResponse[string]{Code: "failed"}
	if failed.IsSuccess() {
		t.Errorf("expected false for non-success code")
	}
}

func TestImageRequest_MarshalJSON_EmptyExtra(t *testing.T) {
	r := ImageRequest{Model: "dall-e-3", Prompt: "cat", N: 1}
	out, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if m["model"] != "dall-e-3" || m["prompt"] != "cat" {
		t.Errorf("marshal mismatch: %+v", m)
	}
}
