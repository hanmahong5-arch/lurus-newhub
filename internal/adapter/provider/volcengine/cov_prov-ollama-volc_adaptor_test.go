package volcengine

import (
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	channelconstant "github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

const provOllamaVolcTestBaseURL = "https://ark.cn-beijing.volces.com"

// ---------------------------------------------------------------------------
// Adaptor.GetRequestURL: this is the money-critical routing table — a wrong
// endpoint silently sends billable traffic to the wrong upstream path.
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_VE_GetRequestURL(t *testing.T) {
	tests := []struct {
		name    string
		info    *relaycommon.RelayInfo
		want    string
		wantErr bool
	}{
		{
			name: "claude format, no special base, regular model -> /api/v3/chat/completions",
			info: &relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatClaude,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: provOllamaVolcTestBaseURL, UpstreamModelName: "doubao-pro-32k"},
			},
			want: provOllamaVolcTestBaseURL + "/api/v3/chat/completions",
		},
		{
			name: "claude format, model prefixed with 'bot' -> bots endpoint",
			info: &relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatClaude,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: provOllamaVolcTestBaseURL, UpstreamModelName: "bot-12345"},
			},
			want: provOllamaVolcTestBaseURL + "/api/v3/bots/chat/completions",
		},
		{
			name: "claude format, special base plan with ClaudeBaseURL -> dedicated /v1/messages",
			info: &relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatClaude,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "doubao-coding-plan"},
			},
			want: "https://ark.cn-beijing.volces.com/api/coding/v1/messages",
		},
		{
			name: "default format, chat completions, regular model",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: provOllamaVolcTestBaseURL, UpstreamModelName: "doubao-pro-32k"},
			},
			want: provOllamaVolcTestBaseURL + "/api/v3/chat/completions",
		},
		{
			name: "default format, chat completions, bot-prefixed model",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: provOllamaVolcTestBaseURL, UpstreamModelName: "bot-xyz"},
			},
			want: provOllamaVolcTestBaseURL + "/api/v3/bots/chat/completions",
		},
		{
			name: "default format, chat completions, special plan with OpenAIBaseURL",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "doubao-coding-plan"},
			},
			want: "https://ark.cn-beijing.volces.com/api/coding/v3/chat/completions",
		},
		{
			name: "embeddings mode",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeEmbeddings,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: provOllamaVolcTestBaseURL},
			},
			want: provOllamaVolcTestBaseURL + "/api/v3/embeddings",
		},
		{
			name: "images generations mode",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeImagesGenerations,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: provOllamaVolcTestBaseURL},
			},
			want: provOllamaVolcTestBaseURL + "/api/v3/images/generations",
		},
		{
			name: "images edits mode also routes to generations (doubao has no dedicated edits endpoint)",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeImagesEdits,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: provOllamaVolcTestBaseURL},
			},
			want: provOllamaVolcTestBaseURL + "/api/v3/images/generations",
		},
		{
			name: "rerank mode",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeRerank,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: provOllamaVolcTestBaseURL},
			},
			want: provOllamaVolcTestBaseURL + "/api/v3/rerank",
		},
		{
			name: "responses mode",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeResponses,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: provOllamaVolcTestBaseURL},
			},
			want: provOllamaVolcTestBaseURL + "/api/v3/responses",
		},
		{
			name: "audio speech on the default VolcEngine base -> dedicated websocket TTS endpoint",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeAudioSpeech,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: channelconstant.ChannelBaseURLs[channelconstant.ChannelTypeVolcEngine]},
			},
			want: "wss://openspeech.bytedance.com/api/v1/tts/ws_binary",
		},
		{
			name: "audio speech on a custom base -> plain REST speech endpoint (not the websocket)",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeAudioSpeech,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://my-custom-proxy.example.com"},
			},
			want: "https://my-custom-proxy.example.com/v1/audio/speech",
		},
		{
			name: "empty base_url falls back to the VolcEngine default",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: ""},
			},
			want: provOllamaVolcTestBaseURL + "/api/v3/chat/completions",
		},
		{
			name: "unsupported relay mode returns an explicit error, not a guessed URL",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeUnknown,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: provOllamaVolcTestBaseURL},
			},
			wantErr: true,
		},
	}

	a := &Adaptor{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := a.GetRequestURL(tt.info)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("GetRequestURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Adaptor.SetupRequestHeader: distinct auth-header shapes per relay mode.
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_VE_SetupRequestHeader_DefaultBearer(t *testing.T) {
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-doubao-key"}}
	header := &http.Header{}
	if err := a.SetupRequestHeader(c, header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Authorization"); got != "Bearer sk-doubao-key" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer sk-doubao-key")
	}
}

func TestProvOllamaVolc_VE_SetupRequestHeader_AudioSpeech_UsesSecondTokenSegment(t *testing.T) {
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/audio/speech", nil)

	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeAudioSpeech, ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "appid123|accesstoken456"}}
	header := &http.Header{}
	if err := a.SetupRequestHeader(c, header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Authorization"); got != "Bearer;accesstoken456" {
		t.Errorf("Authorization = %q, want %q (TTS auth uses the token half only, semicolon-separated)", got, "Bearer;accesstoken456")
	}
	if got := header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json for TTS requests", got)
	}
}

func TestProvOllamaVolc_VE_SetupRequestHeader_AudioSpeech_MalformedApiKey_SendsNoAuthHeader(t *testing.T) {
	// FINDING: SetupRequestHeader's audio-speech branch returns unconditionally
	// (`return nil`) after setting Content-Type, regardless of whether the
	// ApiKey successfully split into 2 pipe-separated parts. If ApiKey doesn't
	// contain exactly one "|" (e.g. an operator pastes a plain access token
	// without the required "appid|token" format), the inner
	// `req.Set("Authorization", ...)` is skipped, and because of the early
	// return the fallback `req.Set("Authorization", "Bearer "+info.ApiKey)`
	// at the bottom of the function never runs either. The request is sent
	// upstream with NO Authorization header at all instead of surfacing a
	// clear "malformed API key" error at setup time — this silently fails
	// open into an upstream 401/403 rather than failing fast locally.
	// Locking in current behavior as a regression baseline.
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/audio/speech", nil)

	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeAudioSpeech, ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "no-pipe-here"}}
	header := &http.Header{}
	if err := a.SetupRequestHeader(c, header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want empty (see FINDING above: malformed key silently drops all auth)", got)
	}
	if got := header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json even when the key is malformed", got)
	}
}

func TestProvOllamaVolc_VE_SetupRequestHeader_ImagesEdits_SetsJSONContentType(t *testing.T) {
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/images/edits", nil)

	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeImagesEdits, ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "k"}}
	header := &http.Header{}
	if err := a.SetupRequestHeader(c, header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Content-Type"); got != gin.MIMEJSON {
		t.Errorf("Content-Type = %q, want %q", got, gin.MIMEJSON)
	}
	if got := header.Get("Authorization"); got != "Bearer k" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer k")
	}
}

// ---------------------------------------------------------------------------
// Adaptor: not-implemented and trivial passthrough methods
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_VE_ConvertGeminiRequest_NotImplemented(t *testing.T) {
	a := &Adaptor{}
	if _, err := a.ConvertGeminiRequest(&gin.Context{}, &relaycommon.RelayInfo{}, &dto.GeminiChatRequest{}); err == nil {
		t.Error("expected an error (not implemented)")
	}
}

func TestProvOllamaVolc_VE_ConvertRerankRequest_ReturnsNilNil(t *testing.T) {
	a := &Adaptor{}
	resp, err := a.ConvertRerankRequest(&gin.Context{}, 0, dto.RerankRequest{})
	if resp != nil || err != nil {
		t.Errorf("ConvertRerankRequest = (%v, %v), want (nil, nil)", resp, err)
	}
}

func TestProvOllamaVolc_VE_ConvertEmbeddingRequest_Passthrough(t *testing.T) {
	a := &Adaptor{}
	req := dto.EmbeddingRequest{Model: "doubao-embedding", Input: "hello"}
	out, err := a.ConvertEmbeddingRequest(&gin.Context{}, &relaycommon.RelayInfo{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := out.(dto.EmbeddingRequest)
	if !ok || got.Model != "doubao-embedding" {
		t.Errorf("ConvertEmbeddingRequest() = %v, want the request passed through verbatim", out)
	}
}

func TestProvOllamaVolc_VE_ConvertOpenAIResponsesRequest_Passthrough(t *testing.T) {
	a := &Adaptor{}
	req := dto.OpenAIResponsesRequest{Model: "doubao-pro-32k"}
	out, err := a.ConvertOpenAIResponsesRequest(&gin.Context{}, &relaycommon.RelayInfo{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := out.(dto.OpenAIResponsesRequest)
	if !ok || got.Model != "doubao-pro-32k" {
		t.Errorf("ConvertOpenAIResponsesRequest() = %v, want the request passed through verbatim", out)
	}
}

func TestProvOllamaVolc_VE_ModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	list := a.GetModelList()
	if len(list) == 0 {
		t.Fatal("GetModelList() should return at least one model")
	}
	if a.GetChannelName() != "volcengine" {
		t.Errorf("GetChannelName() = %q, want %q", a.GetChannelName(), "volcengine")
	}
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertImageRequest: relay-mode dispatch
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_VE_ConvertImageRequest_GenerationsPassesThrough(t *testing.T) {
	a := &Adaptor{}
	req := dto.ImageRequest{Model: "doubao-seedream", Prompt: "a cat"}
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeImagesGenerations}
	out, err := a.ConvertImageRequest(&gin.Context{}, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := out.(dto.ImageRequest)
	if !ok || got.Prompt != "a cat" {
		t.Errorf("ConvertImageRequest() = %v, want passthrough with Prompt=%q", out, "a cat")
	}
}

func TestProvOllamaVolc_VE_ConvertImageRequest_DefaultModePassesThrough(t *testing.T) {
	a := &Adaptor{}
	req := dto.ImageRequest{Model: "doubao-seedream", Prompt: "a dog"}
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeImagesEdits}
	out, err := a.ConvertImageRequest(&gin.Context{}, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := out.(dto.ImageRequest)
	if !ok || got.Prompt != "a dog" {
		t.Errorf("ConvertImageRequest() = %v, want passthrough (images/edits falls to default case since it's commented out)", out)
	}
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertOpenAIRequest: the deepseek "-thinking" suffix rewrite is
// model-routing logic with real billing impact (it mutates the upstream
// model name and injects a THINKING param).
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_VE_ConvertOpenAIRequest_NilRequestErrors(t *testing.T) {
	a := &Adaptor{}
	out, err := a.ConvertOpenAIRequest(&gin.Context{}, &relaycommon.RelayInfo{}, nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
	if out != nil {
		t.Errorf("out = %v, want nil", out)
	}
}

func TestProvOllamaVolc_VE_ConvertOpenAIRequest_DeepseekThinkingSuffixStripped(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "deepseek-v3-thinking"}}
	req := &dto.GeneralOpenAIRequest{Model: "deepseek-v3-thinking"}
	out, err := a.ConvertOpenAIRequest(&gin.Context{}, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := out.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("expected *dto.GeneralOpenAIRequest, got %T", out)
	}
	if got.Model != "deepseek-v3" {
		t.Errorf("Model = %q, want %q (the -thinking suffix must be stripped)", got.Model, "deepseek-v3")
	}
	if info.UpstreamModelName != "deepseek-v3" {
		t.Errorf("info.UpstreamModelName = %q, want %q", info.UpstreamModelName, "deepseek-v3")
	}
	if string(got.THINKING) != `{"type": "enabled"}` {
		t.Errorf("THINKING = %s, want the enabled marker so the upstream still receives extended thinking", got.THINKING)
	}
}

func TestProvOllamaVolc_VE_ConvertOpenAIRequest_NonDeepseekModelUntouched(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "doubao-pro-32k"}}
	req := &dto.GeneralOpenAIRequest{Model: "doubao-pro-32k"}
	out, err := a.ConvertOpenAIRequest(&gin.Context{}, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out.(*dto.GeneralOpenAIRequest)
	if got.Model != "doubao-pro-32k" {
		t.Errorf("Model = %q, want unchanged %q", got.Model, "doubao-pro-32k")
	}
	if len(got.THINKING) != 0 {
		t.Errorf("THINKING = %s, want empty (not injected for non-deepseek models)", got.THINKING)
	}
}

func TestProvOllamaVolc_VE_ConvertOpenAIRequest_DeepseekWithoutThinkingSuffixUntouched(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "deepseek-v3"}}
	req := &dto.GeneralOpenAIRequest{Model: "deepseek-v3"}
	out, err := a.ConvertOpenAIRequest(&gin.Context{}, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out.(*dto.GeneralOpenAIRequest)
	if got.Model != "deepseek-v3" {
		t.Errorf("Model = %q, want unchanged %q", got.Model, "deepseek-v3")
	}
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertClaudeRequest: dispatch between the Claude-native and
// OpenAI-compatible converters based on special-base membership.
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_VE_ConvertClaudeRequest_DefaultRoutesThroughOpenAIAdaptor(t *testing.T) {
	a := &Adaptor{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: provOllamaVolcTestBaseURL}}
	req := &dto.ClaudeRequest{
		Model:     "doubao-pro-32k",
		MaxTokens: 50,
		Messages:  []dto.ClaudeMessage{{Role: "user", Content: "hi"}},
	}
	out, err := a.ConvertClaudeRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The OpenAI adaptor's ConvertClaudeRequest returns *dto.GeneralOpenAIRequest.
	general, ok := out.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("expected *dto.GeneralOpenAIRequest (OpenAI-compatible conversion) for a non-special base, got %T", out)
	}
	if general.MaxTokens != 50 {
		t.Errorf("MaxTokens = %d, want 50 preserved from the Claude request", general.MaxTokens)
	}
}
