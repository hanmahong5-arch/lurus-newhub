package minimax

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	channelconstant "github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func newProvLongtailMinimaxCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", nil)
	return c, w
}

// ---------------------------------------------------------------------------
// GetRequestURL: mode-specific paths + default-base-url fallback.
// ---------------------------------------------------------------------------

func TestMinimax_GetRequestURL_ChatCompletions(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.minimax.chat"},
	}
	got, err := GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.minimax.chat/v1/text/chatcompletion_v2" {
		t.Errorf("GetRequestURL = %q, want %q", got, "https://api.minimax.chat/v1/text/chatcompletion_v2")
	}
}

func TestMinimax_GetRequestURL_AudioSpeech(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeAudioSpeech,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.minimax.chat"},
	}
	got, err := GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.minimax.chat/v1/t2a_v2" {
		t.Errorf("GetRequestURL = %q, want %q", got, "https://api.minimax.chat/v1/t2a_v2")
	}
}

func TestMinimax_GetRequestURL_EmptyBaseUrl_UsesChannelDefault(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: ""},
	}
	got, err := GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantBase := channelconstant.ChannelBaseURLs[channelconstant.ChannelTypeMiniMax]
	want := wantBase + "/v1/text/chatcompletion_v2"
	if got != want {
		t.Errorf("GetRequestURL = %q, want %q (fell back to the registered MiniMax base URL)", got, want)
	}
}

func TestMinimax_GetRequestURL_UnsupportedMode(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeEmbeddings,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.minimax.chat"},
	}
	got, err := GetRequestURL(info)
	if err == nil {
		t.Fatal("expected error for unsupported relay mode (minimax only supports chat + tts)")
	}
	if got != "" {
		t.Errorf("got = %q, want empty string on error", got)
	}
}

// ---------------------------------------------------------------------------
// Adaptor.GetRequestURL delegates to the package function.
// ---------------------------------------------------------------------------

func TestMinimax_Adaptor_GetRequestURL_Delegates(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeAudioSpeech,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://x"},
	}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://x/v1/t2a_v2" {
		t.Errorf("got %q, want %q", got, "https://x/v1/t2a_v2")
	}
}

// ---------------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------------

func TestMinimax_SetupRequestHeader_BearerAuth(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMinimaxCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "mm-secret"}}
	header := http.Header{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Authorization"); got != "Bearer mm-secret" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer mm-secret")
	}
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest / ConvertEmbeddingRequest / ConvertImageRequest /
// ConvertRerankRequest: pass-through business contracts.
// ---------------------------------------------------------------------------

func TestMinimax_ConvertOpenAIRequest_NilRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMinimaxCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertOpenAIRequest(c, info, nil); err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestMinimax_ConvertOpenAIRequest_PassThrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMinimaxCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "abab6.5-chat"}
	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != req {
		t.Error("expected the same pointer to pass through")
	}
}

func TestMinimax_ConvertEmbeddingRequest_PassThrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMinimaxCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.EmbeddingRequest{Model: "embo-01"}
	got, err := a.ConvertEmbeddingRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.(dto.EmbeddingRequest).Model != "embo-01" {
		t.Errorf("got %#v, want pass-through", got)
	}
}

func TestMinimax_ConvertImageRequest_PassThrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMinimaxCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.ImageRequest{Model: "image-01", Prompt: "p"}
	got, err := a.ConvertImageRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.(dto.ImageRequest).Prompt != "p" {
		t.Errorf("got %#v, want pass-through", got)
	}
}

func TestMinimax_ConvertRerankRequest_UnsupportedNilNil(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMinimaxCtx()
	got, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
	if err != nil || got != nil {
		t.Errorf("ConvertRerankRequest = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestMinimax_UnimplementedSurfaces(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMinimaxCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertGeminiRequest(c, info, nil); err == nil {
		t.Error("ConvertGeminiRequest should error")
	}
	if _, err := a.ConvertClaudeRequest(c, info, nil); err == nil {
		t.Error("ConvertClaudeRequest should error")
	}
	if _, err := a.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{}); err == nil {
		t.Error("ConvertOpenAIResponsesRequest should error")
	}
	a.Init(info)
}

func TestMinimax_ModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	if a.GetChannelName() != "minimax" {
		t.Errorf("GetChannelName() = %q, want minimax", a.GetChannelName())
	}
	if len(a.GetModelList()) == 0 {
		t.Fatal("GetModelList() must not be empty")
	}
}

// ---------------------------------------------------------------------------
// ConvertAudioRequest: TTS request construction is the money-adjacent
// surface here (usage is billed by character count later); wrong mode
// guard, voice/speed mapping, and metadata-driven field overrides all
// matter.
// ---------------------------------------------------------------------------

func TestMinimax_ConvertAudioRequest_WrongRelayModeErrors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMinimaxCtx()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{}}
	_, err := a.ConvertAudioRequest(c, info, dto.AudioRequest{})
	if err == nil {
		t.Fatal("expected error when relay mode is not audio-speech")
	}
}

func TestMinimax_ConvertAudioRequest_BuildsRequestBody(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMinimaxCtx()
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeAudioSpeech,
		OriginModelName: "speech-01-hd",
		ChannelMeta:     &relaycommon.ChannelMeta{},
	}
	req := dto.AudioRequest{Input: "hello world", Voice: "male-qn-qingse", Speed: 1.5, ResponseFormat: "mp3"}
	got, err := a.ConvertAudioRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body, err := io.ReadAll(got)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var parsed MiniMaxTTSRequest
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("body not valid JSON: %v", err)
	}
	if parsed.Model != "speech-01-hd" {
		t.Errorf("Model = %q, want %q (from info.OriginModelName)", parsed.Model, "speech-01-hd")
	}
	if parsed.Text != "hello world" {
		t.Errorf("Text = %q, want %q", parsed.Text, "hello world")
	}
	if parsed.VoiceSetting.VoiceID != "male-qn-qingse" {
		t.Errorf("VoiceID = %q, want %q", parsed.VoiceSetting.VoiceID, "male-qn-qingse")
	}
	if parsed.VoiceSetting.Speed != 1.5 {
		t.Errorf("Speed = %v, want 1.5", parsed.VoiceSetting.Speed)
	}
	if parsed.AudioSetting == nil || parsed.AudioSetting.Format != "mp3" {
		t.Errorf("AudioSetting = %+v, want Format=mp3", parsed.AudioSetting)
	}
	if parsed.OutputFormat != "mp3" {
		t.Errorf("OutputFormat = %q, want mp3", parsed.OutputFormat)
	}
	// non-hex format => c should carry "url" for downstream response handling
	if got := c.GetString("response_format"); got != "url" {
		t.Errorf("context response_format = %q, want url (non-hex formats redirect to a URL)", got)
	}
}

func TestMinimax_ConvertAudioRequest_HexFormat_SetsHexContextFlag(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMinimaxCtx()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeAudioSpeech, ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.AudioRequest{Input: "hi", ResponseFormat: "hex"}
	_, err := a.ConvertAudioRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := c.GetString("response_format"); got != "hex" {
		t.Errorf("context response_format = %q, want hex", got)
	}
}

func TestMinimax_ConvertAudioRequest_MetadataOverridesFields(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMinimaxCtx()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeAudioSpeech, ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.AudioRequest{
		Input:    "hi",
		Voice:    "female-a",
		Metadata: json.RawMessage(`{"language_boost":"English","stream":true}`),
	}
	got, err := a.ConvertAudioRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body, _ := io.ReadAll(got)
	var parsed MiniMaxTTSRequest
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("body not valid JSON: %v", err)
	}
	if parsed.LanguageBoost != "English" {
		t.Errorf("LanguageBoost = %q, want English (from metadata override)", parsed.LanguageBoost)
	}
	if !parsed.Stream {
		t.Error("Stream should be true after metadata override")
	}
	// Fields not present in metadata should survive from the base request.
	if parsed.VoiceSetting.VoiceID != "female-a" {
		t.Errorf("VoiceID = %q, want female-a (base field must survive metadata merge)", parsed.VoiceSetting.VoiceID)
	}
}

func TestMinimax_ConvertAudioRequest_MalformedMetadataErrors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMinimaxCtx()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeAudioSpeech, ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.AudioRequest{Input: "hi", Metadata: json.RawMessage(`{not-json`)}
	_, err := a.ConvertAudioRequest(c, info, req)
	if err == nil {
		t.Fatal("expected error for malformed metadata JSON")
	}
}

// ---------------------------------------------------------------------------
// DoResponse dispatch + handleTTSResponse: URL-redirect vs hex-decode paths,
// usage.TotalTokens carrying the billed character count, and upstream error
// surfaces (base_resp status code, empty audio).
// ---------------------------------------------------------------------------

func TestMinimax_DoResponse_NonSpeechMode_DelegatesToOpenAIAdaptor(t *testing.T) {
	a := &Adaptor{}
	c, w := newProvLongtailMinimaxCtx()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if _, ok := usage.(*dto.Usage); !ok {
		t.Fatalf("expected chat completions to be handled by the shared openai adaptor (*dto.Usage), got %T", usage)
	}
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestMinimax_HandleTTSResponse_HexAudio_UsageIsCharacterCount(t *testing.T) {
	c, w := newProvLongtailMinimaxCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	info.SetEstimatePromptTokens(3)
	audioBytes := []byte{0x01, 0x02, 0x03, 0x04}
	body := `{"data":{"audio":"` + hex.EncodeToString(audioBytes) + `","status":2},"extra_info":{"usage_characters":42},"trace_id":"t1","base_resp":{"status_code":0,"status_msg":"success"}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}

	usage, apiErr := handleTTSResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage, got %T", usage)
	}
	if u.TotalTokens != 42 {
		t.Errorf("TotalTokens = %d, want 42 (billed usage_characters from upstream, NOT computed from prompt+completion)", u.TotalTokens)
	}
	if u.PromptTokens != 3 {
		t.Errorf("PromptTokens = %d, want 3 (estimate baseline)", u.PromptTokens)
	}
	if u.CompletionTokens != 0 {
		t.Errorf("CompletionTokens = %d, want 0 (TTS has no completion-token concept)", u.CompletionTokens)
	}
	if w.Body.Len() == 0 {
		t.Fatal("expected raw audio bytes written to the response body")
	}
	if !bytes.Equal(w.Body.Bytes(), audioBytes) {
		t.Errorf("response body = %x, want decoded hex audio %x", w.Body.Bytes(), audioBytes)
	}
}

func TestMinimax_HandleTTSResponse_URLAudio_Redirects(t *testing.T) {
	c, w := newProvLongtailMinimaxCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"data":{"audio":"https://cdn.minimax.chat/audio/x.mp3","status":2},"extra_info":{"usage_characters":10},"base_resp":{"status_code":0}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}

	usage, apiErr := handleTTSResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage.(*dto.Usage).TotalTokens != 10 {
		t.Errorf("TotalTokens = %d, want 10", usage.(*dto.Usage).TotalTokens)
	}
	// gin only flushes the buffered status code to the underlying
	// ResponseWriter on Write() or when the engine finishes the request
	// cycle; since we call the handler directly (bypassing engine.ServeHTTP),
	// force the flush here to observe what a real request would send.
	c.Writer.WriteHeaderNow()
	if w.Code != http.StatusFound {
		t.Errorf("status = %d, want 302 redirect for URL-form audio", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "https://cdn.minimax.chat/audio/x.mp3" {
		t.Errorf("Location = %q, want the upstream audio URL", loc)
	}
}

func TestMinimax_HandleTTSResponse_UpstreamStatusError(t *testing.T) {
	c, _ := newProvLongtailMinimaxCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"data":{"audio":""},"base_resp":{"status_code":1004,"status_msg":"invalid api key"}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
	usage, apiErr := handleTTSResponse(c, resp, info)
	if apiErr == nil {
		t.Fatal("expected error for non-zero base_resp.status_code")
	}
	if usage != nil {
		t.Errorf("usage should be nil on upstream error, got %v", usage)
	}
	if !strings.Contains(apiErr.Error(), "invalid api key") {
		t.Errorf("error = %q, want upstream status_msg propagated", apiErr.Error())
	}
}

func TestMinimax_HandleTTSResponse_EmptyAudioErrors(t *testing.T) {
	c, _ := newProvLongtailMinimaxCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"data":{"audio":""},"base_resp":{"status_code":0}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
	_, apiErr := handleTTSResponse(c, resp, info)
	if apiErr == nil {
		t.Fatal("expected error when upstream returns no audio data despite status_code=0")
	}
}

func TestMinimax_HandleTTSResponse_InvalidHexAudioErrors(t *testing.T) {
	c, _ := newProvLongtailMinimaxCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"data":{"audio":"not-hex-zz"},"base_resp":{"status_code":0}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
	_, apiErr := handleTTSResponse(c, resp, info)
	if apiErr == nil {
		t.Fatal("expected error for audio payload that is neither a URL nor valid hex")
	}
}

func TestMinimax_HandleTTSResponse_MalformedJSON(t *testing.T) {
	c, _ := newProvLongtailMinimaxCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("not json"))}
	_, apiErr := handleTTSResponse(c, resp, info)
	if apiErr == nil {
		t.Fatal("expected error for malformed body")
	}
}

// ---------------------------------------------------------------------------
// handleChatCompletionResponse: currently unused by DoResponse (which
// delegates to the shared openai adaptor instead), but it's a real
// reachable function in the package's public surface, so we lock its
// pass-through-with-headers contract.
// ---------------------------------------------------------------------------

func TestMinimax_HandleChatCompletionResponse_PassesThroughBodyAndHeaders(t *testing.T) {
	c, w := newProvLongtailMinimaxCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	upstreamHeader := http.Header{}
	upstreamHeader.Set("X-Trace-Id", "abc123")
	body := `{"choices":[]}`
	resp := &http.Response{StatusCode: 201, Body: io.NopCloser(strings.NewReader(body)), Header: upstreamHeader}

	usage, apiErr := handleChatCompletionResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil (this path does no token accounting)", usage)
	}
	if w.Code != 201 {
		t.Errorf("status = %d, want upstream status 201 forwarded", w.Code)
	}
	if got := w.Header().Get("X-Trace-Id"); got != "abc123" {
		t.Errorf("X-Trace-Id header = %q, want forwarded from upstream", got)
	}
	if w.Body.String() != body {
		t.Errorf("body = %q, want raw upstream body %q", w.Body.String(), body)
	}
}
