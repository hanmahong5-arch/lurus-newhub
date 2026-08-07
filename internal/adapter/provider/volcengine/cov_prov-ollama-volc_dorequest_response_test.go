package volcengine

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/app"
	channelconstant "github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// Adaptor.DoRequest: the audio-speech + default-base + stream combo must
// short-circuit (the actual upstream call happens later, inside DoResponse's
// websocket branch) rather than hitting the REST transport.
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_VE_DoRequest_AudioSpeechStreamDefaultBase_ShortCircuitsNilNil(t *testing.T) {
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/audio/speech", strings.NewReader(`{}`))

	info := &relaycommon.RelayInfo{
		IsStream:  true,
		RelayMode: constant.RelayModeAudioSpeech,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: channelconstant.ChannelBaseURLs[channelconstant.ChannelTypeVolcEngine],
		},
	}
	resp, err := a.DoRequest(c, info, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != nil {
		t.Errorf("resp = %v, want nil (the websocket dial happens later, in DoResponse)", resp)
	}
}

func TestProvOllamaVolc_VE_DoRequest_AudioSpeechNonStreamCustomBase_HitsRESTUpstream(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"code":3000,"message":"OK","data":""}`))
	}))
	defer srv.Close()

	app.InitHttpClient()
	fs := system_setting.GetFetchSetting()
	prevAllow := fs.AllowPrivateIp
	fs.AllowPrivateIp = true
	defer func() { system_setting.GetFetchSetting().AllowPrivateIp = prevAllow }()

	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/audio/speech", strings.NewReader(`{}`))

	info := &relaycommon.RelayInfo{
		IsStream:  false,
		RelayMode: constant.RelayModeAudioSpeech,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: srv.URL, // NOT the default VolcEngine base -> goes through REST, not websocket.
			ApiKey:         "k",
		},
	}
	resp, err := a.DoRequest(c, info, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	httpResp, ok := resp.(*http.Response)
	if !ok || httpResp == nil {
		t.Fatalf("expected *http.Response for a non-stream audio request on a custom base, got %T", resp)
	}
	defer httpResp.Body.Close()
	if gotPath != "/v1/audio/speech" {
		t.Errorf("upstream received path = %q, want /v1/audio/speech", gotPath)
	}
}

func TestProvOllamaVolc_VE_DoRequest_ChatCompletions_HitsChatUpstream(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"x","choices":[]}`))
	}))
	defer srv.Close()

	app.InitHttpClient()
	fs := system_setting.GetFetchSetting()
	prevAllow := fs.AllowPrivateIp
	fs.AllowPrivateIp = true
	defer func() { system_setting.GetFetchSetting().AllowPrivateIp = prevAllow }()

	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"doubao-pro-32k"}`))

	info := &relaycommon.RelayInfo{
		RelayMode: constant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    srv.URL,
			ApiKey:            "doubao-key",
			UpstreamModelName: "doubao-pro-32k",
		},
	}
	resp, err := a.DoRequest(c, info, strings.NewReader(`{"model":"doubao-pro-32k"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	httpResp := resp.(*http.Response)
	defer httpResp.Body.Close()
	if gotPath != "/api/v3/chat/completions" {
		t.Errorf("upstream received path = %q, want /api/v3/chat/completions", gotPath)
	}
	if gotAuth != "Bearer doubao-key" {
		t.Errorf("upstream received Authorization = %q, want %q", gotAuth, "Bearer doubao-key")
	}
}

// ---------------------------------------------------------------------------
// Adaptor.DoResponse: dispatch table across Claude passthrough / TTS / the
// default OpenAI-compatible chat completions path — each is a distinct
// billing code path with its own usage-extraction logic.
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_VE_DoResponse_ClaudeSpecialBase_NonStream_DelegatesToClaudeHandler(t *testing.T) {
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	body := `{"id":"msg_ve","type":"message","content":[{"type":"text","text":"answer"}],"stop_reason":"end_turn","model":"claude-3-opus-20240229","usage":{"input_tokens":11,"output_tokens":4}}`
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "doubao-coding-plan"}, // a registered ChannelSpecialBases key
	}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage, got %T", usage)
	}
	if u.PromptTokens != 11 || u.CompletionTokens != 4 {
		t.Errorf("usage = %+v, want PromptTokens=11 CompletionTokens=4 straight from the Claude handler", u)
	}
}

func TestProvOllamaVolc_VE_DoResponse_ClaudeFormat_NonSpecialBase_FallsThroughToOpenAIPath(t *testing.T) {
	// Claude format but the base isn't in ChannelSpecialBases -> the special-case
	// `if _, ok := ...; ok` guard is false, so execution falls through to the
	// generic openai.Adaptor{}.DoResponse dispatch below, not the Claude handler.
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	body := `{"id":"chatcmpl-1","object":"chat.completion","model":"doubao-pro-32k","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://ark.cn-beijing.volces.com"},
	}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage from the OpenAI-compatible fallback, got %T", usage)
	}
	if u.PromptTokens != 5 || u.CompletionTokens != 2 {
		t.Errorf("usage = %+v, want PromptTokens=5 CompletionTokens=2 from the OpenAI-format body", u)
	}
}

func TestProvOllamaVolc_VE_DoResponse_Default_DelegatesToOpenAIHandler(t *testing.T) {
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	body := `{"id":"chatcmpl-2","object":"chat.completion","model":"doubao-pro-32k","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":9,"total_tokens":29}}`
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{
		RelayMode:   constant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage, got %T", usage)
	}
	if u.PromptTokens != 20 || u.CompletionTokens != 9 || u.TotalTokens != 29 {
		t.Errorf("usage = %+v, want PromptTokens=20 CompletionTokens=9 TotalTokens=29 (this is the billed quantity)", u)
	}
}

func TestProvOllamaVolc_VE_DoResponse_AudioSpeech_NonStream_DelegatesToTTSHandler(t *testing.T) {
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/audio/speech", nil)

	audioB64 := "YXVkaW8tYnl0ZXM=" // base64("audio-bytes")
	body := `{"reqid":"r","code":3000,"message":"OK","data":"` + audioB64 + `"}`
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeAudioSpeech, ChannelMeta: &relaycommon.ChannelMeta{}}
	info.SetEstimatePromptTokens(4)

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	u := usage.(*dto.Usage)
	if u.PromptTokens != 4 {
		t.Errorf("usage.PromptTokens = %d, want 4 (from the estimated prompt tokens)", u.PromptTokens)
	}
	if w.Body.String() != "audio-bytes" {
		t.Errorf("response body = %q, want the decoded audio bytes", w.Body.String())
	}
}

func TestProvOllamaVolc_VE_DoResponse_AudioSpeech_Stream_MissingTTSRequestInContext_Errors(t *testing.T) {
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/audio/speech", nil)
	// contextKeyTTSRequest deliberately not set on c — simulates a code path
	// bug where DoResponse is invoked without DoRequest having run first.

	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}
	info := &relaycommon.RelayInfo{
		IsStream:    true,
		RelayMode:   constant.RelayModeAudioSpeech,
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr == nil {
		t.Fatal("expected an error when the TTS request is missing from the gin context")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil on error", usage)
	}
	if apiErr.GetErrorCode() != types.ErrorCodeBadRequestBody {
		t.Errorf("error code = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeBadRequestBody)
	}
}

func TestProvOllamaVolc_VE_DoResponse_AudioSpeech_Stream_WrongContextType_Errors(t *testing.T) {
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/audio/speech", nil)
	c.Set(contextKeyTTSRequest, "not-a-VolcengineTTSRequest") // wrong type stashed

	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}
	info := &relaycommon.RelayInfo{
		IsStream:    true,
		RelayMode:   constant.RelayModeAudioSpeech,
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr == nil {
		t.Fatal("expected an error for a type-mismatched TTS request in context")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil on error", usage)
	}
	if apiErr.GetErrorCode() != types.ErrorCodeBadRequestBody {
		t.Errorf("error code = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeBadRequestBody)
	}
}

// ---------------------------------------------------------------------------
// detectImageMimeType: extension -> MIME mapping used when forwarding image
// upload parts (mask/image) to the upstream doubao image endpoint.
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_DetectImageMimeType(t *testing.T) {
	tests := []struct{ filename, want string }{
		{"photo.jpg", "image/jpeg"},
		{"photo.JPG", "image/jpeg"}, // case-insensitive
		{"photo.jpeg", "image/jpeg"},
		{"icon.png", "image/png"},
		{"icon.webp", "image/webp"},
		{"file.jp2", "image/jpeg"},  // unmapped but jp-prefixed -> treated as jpeg
		{"file.gif", "image/png"},   // unmapped, non-jp -> default png
		{"noextension", "image/png"},
	}
	for _, tt := range tests {
		if got := detectImageMimeType(tt.filename); got != tt.want {
			t.Errorf("detectImageMimeType(%q) = %q, want %q", tt.filename, got, tt.want)
		}
	}
}
