package gemini

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/model_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// ---------------------------------------------------------------------------
// Adaptor.SetupRequestHeader
// ---------------------------------------------------------------------------

func TestAdaptor_SetupRequestHeader_SetsApiKeyHeader(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "secret-key-123"}}
	header := http.Header{}
	err := a.SetupRequestHeader(c, &header, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if header.Get("x-goog-api-key") != "secret-key-123" {
		t.Errorf("x-goog-api-key = %q, want %q", header.Get("x-goog-api-key"), "secret-key-123")
	}
}

// ---------------------------------------------------------------------------
// Adaptor.Init -- documented as a deliberate no-op for the Gemini adaptor
// ---------------------------------------------------------------------------

func TestAdaptor_Init_NoPanic(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	a.Init(info) // must not panic; Gemini adaptor performs no init side effects
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertOpenAIRequest
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertOpenAIRequest_NilRequestErrors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	_, err := a.ConvertOpenAIRequest(c, info, nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
	if !strings.Contains(err.Error(), "request is nil") {
		t.Errorf("error = %q, want 'request is nil'", err.Error())
	}
}

func TestAdaptor_ConvertOpenAIRequest_DelegatesToConversion(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "hi"}}}
	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	geminiReq, ok := got.(*dto.GeminiChatRequest)
	if !ok {
		t.Fatalf("type = %T, want *dto.GeminiChatRequest", got)
	}
	if len(geminiReq.Contents) != 1 || geminiReq.Contents[0].Parts[0].Text != "hi" {
		t.Errorf("Contents = %+v, want single 'hi' content", geminiReq.Contents)
	}
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertClaudeRequest
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertClaudeRequest_ConvertsViaOpenAIBridge(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.ClaudeRequest{
		Model:     "claude-3-opus",
		MaxTokens: 100,
		Messages:  []dto.ClaudeMessage{{Role: "user", Content: "hello claude"}},
	}
	got, err := a.ConvertClaudeRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	geminiReq, ok := got.(*dto.GeminiChatRequest)
	if !ok {
		t.Fatalf("type = %T, want *dto.GeminiChatRequest", got)
	}
	if len(geminiReq.Contents) != 1 {
		t.Fatalf("len(Contents) = %d, want 1", len(geminiReq.Contents))
	}
	if geminiReq.Contents[0].Parts[0].Text != "hello claude" {
		t.Errorf("Text = %q, want %q", geminiReq.Contents[0].Parts[0].Text, "hello claude")
	}
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertAudioRequest
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertAudioRequest_TTSMode(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
		RelayMode:   relayconstant.RelayModeAudioSpeech,
	}
	reader, err := a.ConvertAudioRequest(c, info, dto.AudioRequest{Input: "hello", Voice: "alloy"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reader == nil {
		t.Fatal("reader is nil, want a valid TTS request body")
	}
}

func TestAdaptor_ConvertAudioRequest_NonTTSModeRejected(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
		RelayMode:   0, // not RelayModeAudioSpeech
	}
	_, err := a.ConvertAudioRequest(c, info, dto.AudioRequest{})
	if err == nil {
		t.Fatal("expected error: Gemini adaptor only supports TTS, not transcription")
	}
	if !strings.Contains(err.Error(), "only supports TTS") {
		t.Errorf("error = %q, want mention of TTS-only support", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertEmbeddingRequest
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertEmbeddingRequest_NilInputErrors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	_, err := a.ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{})
	if err == nil {
		t.Fatal("expected error for nil input")
	}
	if !strings.Contains(err.Error(), "input is required") {
		t.Errorf("error = %q, want 'input is required'", err.Error())
	}
}

func TestAdaptor_ConvertEmbeddingRequest_EmptyParsedInputErrors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	// Input is a non-nil value that parses to zero strings (e.g. a JSON number,
	// not string/[]any), which exercises the second "input is empty" gate.
	_, err := a.ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{Input: 42.0})
	if err == nil {
		t.Fatal("expected error for empty parsed input")
	}
	if !strings.Contains(err.Error(), "input is empty") {
		t.Errorf("error = %q, want 'input is empty'", err.Error())
	}
}

func TestAdaptor_ConvertEmbeddingRequest_BuildsBatchPayload(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "text-embedding-004"}}
	got, err := a.ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{
		Input:      []any{"first", "second"},
		Dimensions: 256,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.IsGeminiBatchEmbedding {
		t.Error("IsGeminiBatchEmbedding = false, want true (batch endpoint always used)")
	}
	payload, ok := got.(map[string]interface{})
	if !ok {
		t.Fatalf("type = %T, want map[string]interface{}", got)
	}
	requests, ok := payload["requests"].([]map[string]interface{})
	if !ok {
		t.Fatalf("requests type = %T, want []map[string]interface{}", payload["requests"])
	}
	if len(requests) != 2 {
		t.Fatalf("len(requests) = %d, want 2", len(requests))
	}
	if requests[0]["outputDimensionality"] != 256 {
		t.Errorf("outputDimensionality = %v, want 256 (dimensions supported for text-embedding-004)", requests[0]["outputDimensionality"])
	}
}

func TestAdaptor_ConvertEmbeddingRequest_DimensionsIgnoredForUnsupportedModel(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "embedding-001"}}
	got, err := a.ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{
		Input:      "solo",
		Dimensions: 128,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	payload := got.(map[string]interface{})
	requests := payload["requests"].([]map[string]interface{})
	if _, has := requests[0]["outputDimensionality"]; has {
		t.Errorf("outputDimensionality = %v, want absent for embedding-001 (not in supported list)", requests[0]["outputDimensionality"])
	}
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertRerankRequest / ConvertOpenAIResponsesRequest (stubs)
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertRerankRequest_AlwaysNilNil(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestContext()
	got, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
	if got != nil || err != nil {
		t.Errorf("got (%v, %v), want (nil, nil) -- Gemini has no native rerank support", got, err)
	}
}

func TestAdaptor_ConvertOpenAIResponsesRequest_NotImplemented(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	_, err := a.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{})
	if err == nil {
		t.Fatal("expected 'not implemented' error")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("error = %q, want 'not implemented'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Adaptor.GetRequestURL
// ---------------------------------------------------------------------------

func TestAdaptor_GetRequestURL_Imagen(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelBaseUrl:    "https://generativelanguage.googleapis.com",
		UpstreamModelName: "imagen-3.0-generate-002",
	}}
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://generativelanguage.googleapis.com/v1beta/models/imagen-3.0-generate-002:predict"
	if url != want {
		t.Errorf("url = %q, want %q", url, want)
	}
}

func TestAdaptor_GetRequestURL_TTS(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://api.example.com",
			UpstreamModelName: "gemini-2.5-flash-preview-tts",
		},
		RelayMode: relayconstant.RelayModeAudioSpeech,
	}
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://api.example.com/v1beta/models/gemini-2.5-flash-preview-tts:generateContent"
	if url != want {
		t.Errorf("url = %q, want %q", url, want)
	}
}

func TestAdaptor_GetRequestURL_EmbeddingSingle(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelBaseUrl:    "https://api.example.com",
		UpstreamModelName: "text-embedding-004",
	}}
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://api.example.com/v1beta/models/text-embedding-004:embedContent"
	if url != want {
		t.Errorf("url = %q, want %q", url, want)
	}
}

func TestAdaptor_GetRequestURL_EmbeddingBatch(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://api.example.com",
			UpstreamModelName: "gemini-embedding-exp-03-07",
		},
		IsGeminiBatchEmbedding: true,
	}
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://api.example.com/v1beta/models/gemini-embedding-exp-03-07:batchEmbedContents"
	if url != want {
		t.Errorf("url = %q, want %q", url, want)
	}
}

func TestAdaptor_GetRequestURL_GenerateContent_NonStream(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelBaseUrl:    "https://api.example.com",
		UpstreamModelName: "gemini-2.0-flash",
	}}
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://api.example.com/v1beta/models/gemini-2.0-flash:generateContent"
	if url != want {
		t.Errorf("url = %q, want %q", url, want)
	}
}

func TestAdaptor_GetRequestURL_StreamGenerateContent(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://api.example.com",
			UpstreamModelName: "gemini-2.0-flash",
		},
		IsStream: true,
	}
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://api.example.com/v1beta/models/gemini-2.0-flash:streamGenerateContent?alt=sse"
	if url != want {
		t.Errorf("url = %q, want %q", url, want)
	}
}

func TestAdaptor_GetRequestURL_StreamGeminiNativeDisablesPing(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://api.example.com",
			UpstreamModelName: "gemini-2.0-flash",
		},
		IsStream:  true,
		RelayMode: relayconstant.RelayModeGemini,
	}
	_, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.DisablePing {
		t.Error("DisablePing = false, want true for native Gemini streaming mode")
	}
}

func TestAdaptor_GetRequestURL_VersionOverridePerModel(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	prevVal, hadPrev := settings.VersionSettings["gemini-1.0-pro"]
	settings.VersionSettings["gemini-1.0-pro"] = "v1"
	t.Cleanup(func() {
		if hadPrev {
			settings.VersionSettings["gemini-1.0-pro"] = prevVal
		} else {
			delete(settings.VersionSettings, "gemini-1.0-pro")
		}
	})

	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelBaseUrl:    "https://api.example.com",
		UpstreamModelName: "gemini-1.0-pro",
	}}
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(url, "/v1/models/") {
		t.Errorf("url = %q, want version override 'v1' applied for gemini-1.0-pro", url)
	}
}

func TestAdaptor_GetRequestURL_ThinkingSuffixTrimmed_WhenAdapterEnabled(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	prevEnabled := settings.ThinkingAdapterEnabled
	settings.ThinkingAdapterEnabled = true
	t.Cleanup(func() { settings.ThinkingAdapterEnabled = prevEnabled })

	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelBaseUrl:    "https://api.example.com",
		UpstreamModelName: "gemini-2.0-flash-thinking-500",
	}}
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://api.example.com/v1beta/models/gemini-2.0-flash:generateContent"
	if url != want {
		t.Errorf("url = %q, want %q (budget suffix stripped from upstream model name)", url, want)
	}
	if info.UpstreamModelName != "gemini-2.0-flash" {
		t.Errorf("info.UpstreamModelName = %q, want stripped %q", info.UpstreamModelName, "gemini-2.0-flash")
	}
}

func TestAdaptor_GetRequestURL_NothinkingSuffixTrimmed_WhenAdapterEnabled(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	prevEnabled := settings.ThinkingAdapterEnabled
	settings.ThinkingAdapterEnabled = true
	t.Cleanup(func() { settings.ThinkingAdapterEnabled = prevEnabled })

	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelBaseUrl:    "https://api.example.com",
		UpstreamModelName: "gemini-2.0-flash-nothinking",
	}}
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(url, "/models/gemini-2.0-flash:") {
		t.Errorf("url = %q, want -nothinking suffix stripped", url)
	}
}

func TestAdaptor_GetRequestURL_EffortSuffixTrimmed_WhenAdapterEnabled(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	prevEnabled := settings.ThinkingAdapterEnabled
	settings.ThinkingAdapterEnabled = true
	t.Cleanup(func() { settings.ThinkingAdapterEnabled = prevEnabled })

	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelBaseUrl:    "https://api.example.com",
		UpstreamModelName: "gemini-3-pro-preview-high",
	}}
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(url, "/models/gemini-3-pro-preview:") {
		t.Errorf("url = %q, want reasoning-effort suffix stripped", url)
	}
}

func TestAdaptor_GetRequestURL_ThinkingSuffixUntouched_WhenAdapterDisabled(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	prevEnabled := settings.ThinkingAdapterEnabled
	settings.ThinkingAdapterEnabled = false
	t.Cleanup(func() { settings.ThinkingAdapterEnabled = prevEnabled })

	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelBaseUrl:    "https://api.example.com",
		UpstreamModelName: "gemini-2.0-flash-thinking-500",
	}}
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(url, "gemini-2.0-flash-thinking-500") {
		t.Errorf("url = %q, want suffix preserved when adapter disabled", url)
	}
}

// ---------------------------------------------------------------------------
// Adaptor.DoResponse dispatcher: verifies the correct handler is routed to
// by observing handler-specific side effects (status/body) rather than usage
// numerics alone, since several branches share a similar Usage shape.
// ---------------------------------------------------------------------------

func TestAdaptor_DoResponse_NativeGeminiNonStream(t *testing.T) {
	a := &Adaptor{}
	c, w := newTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
		RelayMode:   relayconstant.RelayModeGemini,
	}
	body := `{"candidates":[{"content":{"parts":[{"text":"native"}]},"index":0}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`
	bcResp6 := respFromBody(200, body)
	defer func() {
		if bcResp6 != nil {
			_ = bcResp6.Body.Close()
		}
	}()
	_, apiErr := a.DoResponse(c, bcResp6, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	// native handler passes the body through unchanged (no "choices" re-wrap)
	if w.Body.String() != body {
		t.Errorf("body = %q, want raw native passthrough %q", w.Body.String(), body)
	}
}

func TestAdaptor_DoResponse_NativeGeminiEmbedContentPath(t *testing.T) {
	a := &Adaptor{}
	c, w := newTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta:    &relaycommon.ChannelMeta{},
		RelayMode:      relayconstant.RelayModeGemini,
		RequestURLPath: "/v1beta/models/text-embedding-004:embedContent",
	}
	body := `{"embedding":{"values":[1,2,3]}}`
	bcResp5 := respFromBody(200, body)
	defer func() {
		if bcResp5 != nil {
			_ = bcResp5.Body.Close()
		}
	}()
	_, apiErr := a.DoResponse(c, bcResp5, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if w.Body.String() != body {
		t.Errorf("body = %q, want unchanged native embedding passthrough", w.Body.String())
	}
}

func TestAdaptor_DoResponse_ImagenModel(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-3.0-generate-002"},
	}
	body := `{"predictions":[{"bytesBase64Encoded":"aaaa"}]}`
	bcResp4 := respFromBody(200, body)
	defer func() {
		if bcResp4 != nil {
			_ = bcResp4.Body.Close()
		}
	}()
	usage, apiErr := a.DoResponse(c, bcResp4, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	u := usage.(*dto.Usage)
	if u.PromptTokens != 258 {
		t.Errorf("PromptTokens = %d, want 258 (routed to GeminiImageHandler)", u.PromptTokens)
	}
}

func TestAdaptor_DoResponse_AudioSpeechMode(t *testing.T) {
	a := &Adaptor{}
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
		RelayMode:   relayconstant.RelayModeAudioSpeech,
	}
	// TTS handler surfaces upstream errors distinctly, proving it (not the
	// chat/embedding handler) was the one invoked.
	bcResp3 := respFromBody(500, `boom`)
	defer func() {
		if bcResp3 != nil {
			_ = bcResp3.Body.Close()
		}
	}()
	_, apiErr := a.DoResponse(c, bcResp3, info)
	if apiErr == nil {
		t.Fatal("expected error routed through GeminiTTSHandler for non-200 upstream")
	}
}

func TestAdaptor_DoResponse_EmbeddingModelPrefix(t *testing.T) {
	a := &Adaptor{}
	c, w := newTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "embedding-001"},
	}
	body := `{"embeddings":[{"values":[1,2]}]}`
	bcResp2 := respFromBody(200, body)
	defer func() {
		if bcResp2 != nil {
			_ = bcResp2.Body.Close()
		}
	}()
	_, apiErr := a.DoResponse(c, bcResp2, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	var out dto.OpenAIEmbeddingResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("output not valid JSON: %v (%s)", err, w.Body.String())
	}
	if len(out.Data) != 1 {
		t.Errorf("Data = %+v, want 1 item (routed to GeminiEmbeddingHandler)", out.Data)
	}
}

func TestAdaptor_DoResponse_DefaultChatNonStream(t *testing.T) {
	a := &Adaptor{}
	c, w := newTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"},
		RelayFormat: types.RelayFormatOpenAI,
	}
	body := `{"candidates":[{"content":{"parts":[{"text":"chat"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`
	bcResp1 := respFromBody(200, body)
	defer func() {
		if bcResp1 != nil {
			_ = bcResp1.Body.Close()
		}
	}()
	_, apiErr := a.DoResponse(c, bcResp1, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if !strings.Contains(w.Body.String(), `"choices"`) {
		t.Errorf("body = %q, want OpenAI-shaped chat response (routed to GeminiChatHandler)", w.Body.String())
	}
}

func TestAdaptor_DoResponse_DefaultChatStream(t *testing.T) {
	a := &Adaptor{}
	c, w := newTestContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"},
		RelayFormat: types.RelayFormatOpenAI,
		IsStream:    true,
	}
	body := sseBody(`{"candidates":[{"content":{"parts":[{"text":"s"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`)
	bcResp0 := respFromBody(200, body)
	defer func() {
		if bcResp0 != nil {
			_ = bcResp0.Body.Close()
		}
	}()
	_, apiErr := a.DoResponse(c, bcResp0, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if !strings.Contains(w.Body.String(), "[DONE]") {
		t.Errorf("body = %q, want SSE stream terminated with [DONE] (routed to GeminiChatStreamHandler)", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Adaptor.GetModelList / GetChannelName duplicate coverage intentionally
// omitted (already covered by relay_gemini_test.go); ModelList/constants are
// exercised indirectly through GetRequestURL above.
// ---------------------------------------------------------------------------
