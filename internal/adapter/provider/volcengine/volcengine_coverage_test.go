package volcengine

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	channelconstant "github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newGinContext() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c, w
}

type closerReader struct {
	*strings.Reader
}

func (c *closerReader) Close() error { return nil }

func readCloserFrom(s string) *closerReader {
	return &closerReader{Reader: strings.NewReader(s)}
}

// ---------------------------------------------------------------------------
// GetRequestURL
// ---------------------------------------------------------------------------

func TestGetRequestURL(t *testing.T) {
	defaultBase := channelconstant.ChannelBaseURLs[channelconstant.ChannelTypeVolcEngine]

	tests := []struct {
		name    string
		info    *relaycommon.RelayInfo
		want    string
		wantErr string
	}{
		{
			name: "claude format default base plain model",
			info: &relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatClaude,
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "doubao-pro"},
			},
			want: defaultBase + "/api/v3/chat/completions",
		},
		{
			name: "claude format bot model prefix",
			info: &relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatClaude,
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "bot-123"},
			},
			want: defaultBase + "/api/v3/bots/chat/completions",
		},
		{
			name: "claude format special base plan",
			info: &relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatClaude,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "doubao-coding-plan"},
			},
			want: "https://ark.cn-beijing.volces.com/api/coding/v1/messages",
		},
		{
			name: "openai format chat completions default",
			info: &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "doubao-pro"},
			},
			want: defaultBase + "/api/v3/chat/completions",
		},
		{
			name: "openai format chat completions bot prefix",
			info: &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "bot-abc"},
			},
			want: defaultBase + "/api/v3/bots/chat/completions",
		},
		{
			name: "openai format chat completions special base plan",
			info: &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "doubao-coding-plan"},
			},
			want: "https://ark.cn-beijing.volces.com/api/coding/v3/chat/completions",
		},
		{
			name: "embeddings",
			info: &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeEmbeddings,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://custom.example.com"},
			},
			want: "https://custom.example.com/api/v3/embeddings",
		},
		{
			name: "images generations",
			info: &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeImagesGenerations,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://custom.example.com"},
			},
			want: "https://custom.example.com/api/v3/images/generations",
		},
		{
			name: "images edits routes to generations endpoint too",
			info: &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeImagesEdits,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://custom.example.com"},
			},
			want: "https://custom.example.com/api/v3/images/generations",
		},
		{
			name: "rerank",
			info: &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeRerank,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://custom.example.com"},
			},
			want: "https://custom.example.com/api/v3/rerank",
		},
		{
			name: "responses",
			info: &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeResponses,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://custom.example.com"},
			},
			want: "https://custom.example.com/api/v3/responses",
		},
		{
			name: "audio speech default base url uses websocket endpoint",
			info: &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeAudioSpeech,
				ChannelMeta: &relaycommon.ChannelMeta{},
			},
			want: "wss://openspeech.bytedance.com/api/v1/tts/ws_binary",
		},
		{
			name: "audio speech custom base url uses http endpoint",
			info: &relaycommon.RelayInfo{
				RelayMode:   relayconstant.RelayModeAudioSpeech,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://custom.example.com"},
			},
			want: "https://custom.example.com/v1/audio/speech",
		},
		{
			name: "unsupported relay mode",
			info: &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{},
			},
			wantErr: "unsupported relay mode: 0",
		},
	}

	a := &Adaptor{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := a.GetRequestURL(tt.info)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("GetRequestURL() err = %v, want %q", err, tt.wantErr)
				}
				return
			}
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
	t.Run("chat completions sets bearer with raw api key", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := newGinContext()
		header := http.Header{}
		info := &relaycommon.RelayInfo{
			RelayMode:   relayconstant.RelayModeChatCompletions,
			ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-test"},
		}
		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer sk-test")
		}
	})

	t.Run("images edits sets json content type and bearer", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := newGinContext()
		header := http.Header{}
		info := &relaycommon.RelayInfo{
			RelayMode:   relayconstant.RelayModeImagesEdits,
			ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-test"},
		}
		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Content-Type"); got != gin.MIMEJSON {
			t.Errorf("Content-Type = %q, want %q", got, gin.MIMEJSON)
		}
		if got := header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer sk-test")
		}
	})

	t.Run("audio speech with valid pipe key sets semicolon bearer and returns early", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := newGinContext()
		header := http.Header{}
		info := &relaycommon.RelayInfo{
			RelayMode:   relayconstant.RelayModeAudioSpeech,
			ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "app123|token456"},
		}
		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Authorization"); got != "Bearer;token456" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer;token456")
		}
		if got := header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
	})

	t.Run("audio speech with malformed key skips auth header but still returns early", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := newGinContext()
		header := http.Header{}
		info := &relaycommon.RelayInfo{
			RelayMode:   relayconstant.RelayModeAudioSpeech,
			ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "no-pipe-here"},
		}
		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty (early return skips final Bearer-set line)", got)
		}
		if got := header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
	})
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName / constants
// ---------------------------------------------------------------------------

func TestGetModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	got := a.GetModelList()
	if len(got) != len(ModelList) {
		t.Fatalf("GetModelList() length = %d, want %d", len(got), len(ModelList))
	}
	for i, m := range ModelList {
		if got[i] != m {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, got[i], m)
		}
	}
	if a.GetChannelName() != "volcengine" {
		t.Errorf("GetChannelName() = %q, want volcengine", a.GetChannelName())
	}
	if ChannelName != "volcengine" {
		t.Errorf("ChannelName = %q, want volcengine", ChannelName)
	}
}

// ---------------------------------------------------------------------------
// Convert* stubs / passthroughs
// ---------------------------------------------------------------------------

func TestConvertGeminiRequest_NotImplemented(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertGeminiRequest(nil, nil, nil)
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err == nil || err.Error() != "not implemented" {
		t.Fatalf("expected 'not implemented' error, got %v", err)
	}
}

func TestConvertClaudeRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := newGinContext()

	t.Run("special base delegates to claude adaptor pass-through", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "doubao-coding-plan"},
		}
		req := &dto.ClaudeRequest{Model: "claude-x"}
		got, err := a.ConvertClaudeRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotReq, ok := got.(*dto.ClaudeRequest)
		if !ok || gotReq != req {
			t.Fatalf("expected same *dto.ClaudeRequest pointer returned, got %T %v", got, got)
		}
	})

	t.Run("default base delegates to openai adaptor conversion", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://ark.cn-beijing.volces.com"},
		}
		req := &dto.ClaudeRequest{Model: "doubao-pro"}
		got, err := a.ConvertClaudeRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotReq, ok := got.(*dto.GeneralOpenAIRequest)
		if !ok {
			t.Fatalf("expected *dto.GeneralOpenAIRequest, got %T", got)
		}
		if gotReq.Model != "doubao-pro" {
			t.Errorf("Model = %q, want doubao-pro", gotReq.Model)
		}
	})
}

func TestConvertRerankRequest_ReturnsNilNil(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertRerankRequest(nil, 0, dto.RerankRequest{})
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestConvertEmbeddingRequest_PassThrough(t *testing.T) {
	a := &Adaptor{}
	req := dto.EmbeddingRequest{Model: "Doubao-embedding"}
	got, err := a.ConvertEmbeddingRequest(nil, nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotReq, ok := got.(dto.EmbeddingRequest)
	if !ok || gotReq.Model != "Doubao-embedding" {
		t.Fatalf("expected pass-through EmbeddingRequest, got %T %v", got, got)
	}
}

func TestConvertOpenAIResponsesRequest_PassThrough(t *testing.T) {
	a := &Adaptor{}
	req := dto.OpenAIResponsesRequest{Model: "doubao-pro"}
	got, err := a.ConvertOpenAIResponsesRequest(nil, nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotReq, ok := got.(dto.OpenAIResponsesRequest)
	if !ok || gotReq.Model != "doubao-pro" {
		t.Fatalf("expected pass-through OpenAIResponsesRequest, got %T %v", got, got)
	}
}

func TestInit_NoOp(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://example.com"}}
	a.Init(info)
	if info.ChannelBaseUrl != "https://example.com" {
		t.Errorf("Init unexpectedly mutated info: %+v", info)
	}
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest
// ---------------------------------------------------------------------------

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("nil request errors", func(t *testing.T) {
		got, err := a.ConvertOpenAIRequest(nil, nil, nil)
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "request is nil" {
			t.Fatalf("expected 'request is nil' error, got %v", err)
		}
	})

	t.Run("deepseek thinking suffix rewritten and thinking flag injected", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			OriginModelName: "deepseek-chat-thinking",
			ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "deepseek-chat-thinking"},
		}
		req := &dto.GeneralOpenAIRequest{Model: "deepseek-chat-thinking"}
		got, err := a.ConvertOpenAIRequest(nil, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotReq := got.(*dto.GeneralOpenAIRequest)
		if gotReq.Model != "deepseek-chat" {
			t.Errorf("Model = %q, want deepseek-chat", gotReq.Model)
		}
		if info.UpstreamModelName != "deepseek-chat" {
			t.Errorf("info.UpstreamModelName = %q, want deepseek-chat", info.UpstreamModelName)
		}
		if string(gotReq.THINKING) != `{"type": "enabled"}` {
			t.Errorf("THINKING = %s, want enabled payload", gotReq.THINKING)
		}
	})

	t.Run("non-deepseek model left untouched", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			OriginModelName: "doubao-pro-thinking",
			ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "doubao-pro-thinking"},
		}
		req := &dto.GeneralOpenAIRequest{Model: "doubao-pro-thinking"}
		got, err := a.ConvertOpenAIRequest(nil, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotReq := got.(*dto.GeneralOpenAIRequest)
		if gotReq.Model != "doubao-pro-thinking" {
			t.Errorf("Model = %q, want untouched doubao-pro-thinking", gotReq.Model)
		}
		if len(gotReq.THINKING) != 0 {
			t.Errorf("expected no THINKING payload, got %s", gotReq.THINKING)
		}
	})

	t.Run("deepseek model without thinking suffix left untouched", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			OriginModelName: "deepseek-chat",
			ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "deepseek-chat"},
		}
		req := &dto.GeneralOpenAIRequest{Model: "deepseek-chat"}
		got, err := a.ConvertOpenAIRequest(nil, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotReq := got.(*dto.GeneralOpenAIRequest)
		if gotReq.Model != "deepseek-chat" {
			t.Errorf("Model = %q, want deepseek-chat", gotReq.Model)
		}
	})
}

// ---------------------------------------------------------------------------
// ConvertImageRequest
// ---------------------------------------------------------------------------

func TestConvertImageRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("images generations passes request through unchanged", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesGenerations}
		req := dto.ImageRequest{Model: "doubao-seedream", Prompt: "a cat"}
		got, err := a.ConvertImageRequest(nil, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotReq, ok := got.(dto.ImageRequest)
		if !ok || gotReq.Prompt != "a cat" {
			t.Fatalf("expected pass-through ImageRequest, got %T %v", got, got)
		}
	})

	t.Run("default relay mode also passes request through unchanged", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesEdits}
		req := dto.ImageRequest{Model: "m", Prompt: "p"}
		got, err := a.ConvertImageRequest(nil, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotReq, ok := got.(dto.ImageRequest)
		if !ok || gotReq.Prompt != "p" {
			t.Fatalf("expected pass-through ImageRequest, got %T %v", got, got)
		}
	})
}

// ---------------------------------------------------------------------------
// ConvertAudioRequest
// ---------------------------------------------------------------------------

func TestConvertAudioRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("unsupported relay mode errors", func(t *testing.T) {
		c, _ := newGinContext()
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions}
		got, err := a.ConvertAudioRequest(c, info, dto.AudioRequest{})
		if got != nil {
			t.Errorf("expected nil reader, got %v", got)
		}
		if err == nil || err.Error() != "unsupported audio relay mode" {
			t.Fatalf("expected 'unsupported audio relay mode' error, got %v", err)
		}
	})

	t.Run("invalid api key format errors", func(t *testing.T) {
		c, _ := newGinContext()
		info := &relaycommon.RelayInfo{
			RelayMode:   relayconstant.RelayModeAudioSpeech,
			ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "no-pipe"},
		}
		got, err := a.ConvertAudioRequest(c, info, dto.AudioRequest{})
		if got != nil {
			t.Errorf("expected nil reader, got %v", got)
		}
		if err == nil || !strings.Contains(err.Error(), "invalid api key format") {
			t.Fatalf("expected invalid api key format error, got %v", err)
		}
	})

	t.Run("valid request builds tts payload and sets stream+context", func(t *testing.T) {
		c, _ := newGinContext()
		info := &relaycommon.RelayInfo{
			RelayMode:       relayconstant.RelayModeAudioSpeech,
			OriginModelName: "tts-1",
			ChannelMeta:     &relaycommon.ChannelMeta{ApiKey: "app123|token456"},
		}
		req := dto.AudioRequest{
			Input:          "hello world",
			Voice:          "alloy",
			Speed:          1.5,
			ResponseFormat: "opus",
		}
		reader, err := a.ConvertAudioRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if reader == nil {
			t.Fatalf("expected non-nil reader")
		}
		if !info.IsStream {
			t.Errorf("expected info.IsStream=true after submit operation, got false")
		}
		if got := c.GetString(contextKeyResponseFormat); got != "ogg_opus" {
			t.Errorf("context response format = %q, want ogg_opus", got)
		}

		body, readErr := io.ReadAll(reader)
		if readErr != nil {
			t.Fatalf("failed reading marshalled body: %v", readErr)
		}
		var got VolcengineTTSRequest
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("failed to unmarshal body: %v", err)
		}
		if got.App.AppID != "app123" || got.App.Token != "token456" || got.App.Cluster != "volcano_tts" {
			t.Errorf("unexpected App: %+v", got.App)
		}
		if got.Audio.VoiceType != "zh_male_M392_conversation_wvae_bigtts" {
			t.Errorf("VoiceType = %q, want mapped alloy voice", got.Audio.VoiceType)
		}
		if got.Audio.Encoding != "ogg_opus" {
			t.Errorf("Encoding = %q, want ogg_opus", got.Audio.Encoding)
		}
		if got.Audio.SpeedRatio != 1.5 {
			t.Errorf("SpeedRatio = %v, want 1.5", got.Audio.SpeedRatio)
		}
		if got.Audio.Rate != 24000 {
			t.Errorf("Rate = %d, want 24000", got.Audio.Rate)
		}
		if got.Request.Text != "hello world" || got.Request.Operation != "submit" || got.Request.Model != "tts-1" {
			t.Errorf("unexpected Request info: %+v", got.Request)
		}
		if got.Request.ReqID == "" {
			t.Errorf("expected non-empty ReqID")
		}

		ctxVal, exists := c.Get(contextKeyTTSRequest)
		if !exists {
			t.Fatalf("expected volcengine tts request stored in context")
		}
		ctxReq, ok := ctxVal.(VolcengineTTSRequest)
		if !ok || ctxReq.Request.Text != "hello world" {
			t.Fatalf("unexpected context tts request: %+v", ctxVal)
		}
	})

	t.Run("metadata overrides fields and non-submit operation leaves stream flag alone", func(t *testing.T) {
		c, _ := newGinContext()
		info := &relaycommon.RelayInfo{
			RelayMode:   relayconstant.RelayModeAudioSpeech,
			ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "app|tok"},
		}
		req := dto.AudioRequest{
			Input: "hi",
			Voice: "unknown-voice",
			Metadata: json.RawMessage(`{"request":{"operation":"query","reqid":"fixed-req-id","text":"hi"}}`),
		}
		reader, err := a.ConvertAudioRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.IsStream {
			t.Errorf("expected info.IsStream to remain false for non-submit operation")
		}
		body, _ := io.ReadAll(reader)
		var got VolcengineTTSRequest
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("failed to unmarshal body: %v", err)
		}
		if got.Request.Operation != "query" || got.Request.ReqID != "fixed-req-id" {
			t.Errorf("metadata override not applied: %+v", got.Request)
		}
		// unknown voice falls back to identity mapping.
		if got.Audio.VoiceType != "unknown-voice" {
			t.Errorf("VoiceType = %q, want identity fallback", got.Audio.VoiceType)
		}
		// unspecified response format falls back to default mp3 encoding.
		if got.Audio.Encoding != "mp3" {
			t.Errorf("Encoding = %q, want default mp3", got.Audio.Encoding)
		}
	})

	t.Run("invalid metadata json errors", func(t *testing.T) {
		c, _ := newGinContext()
		info := &relaycommon.RelayInfo{
			RelayMode:   relayconstant.RelayModeAudioSpeech,
			ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "app|tok"},
		}
		req := dto.AudioRequest{
			Input:    "hi",
			Metadata: json.RawMessage(`{not valid json`),
		}
		got, err := a.ConvertAudioRequest(c, info, req)
		if got != nil {
			t.Errorf("expected nil reader on metadata unmarshal error, got %v", got)
		}
		if err == nil || !strings.Contains(err.Error(), "error unmarshalling metadata") {
			t.Fatalf("expected metadata unmarshal error, got %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// DoRequest — hermetic branches only (no live network)
// ---------------------------------------------------------------------------

func TestDoRequest_AudioSpeechStreamDefaultBaseURL_ReturnsNilNil(t *testing.T) {
	a := &Adaptor{}
	c, _ := newGinContext()
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeAudioSpeech,
		IsStream:    true,
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
	got, err := a.DoRequest(c, info, nil)
	if got != nil || err != nil {
		t.Fatalf("expected (nil, nil) for stream+default-base audio speech, got (%v, %v)", got, err)
	}
}

func TestDoRequest_UnsupportedRelayMode_GetRequestURLFails(t *testing.T) {
	a := &Adaptor{}
	c, _ := newGinContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	got, err := a.DoRequest(c, info, nil)
	// Note: got is a (*http.Response)(nil) boxed into an `any` return value,
	// so a plain `got != nil` interface comparison would be true even though
	// the underlying pointer is nil - assert on the error only.
	_ = got
	if err == nil || !strings.Contains(err.Error(), "get request url failed") {
		t.Fatalf("expected 'get request url failed' wrapping error, got %v", err)
	}
}

func TestDoRequest_InvalidMethodFailsAtNewRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := newGinContext()
	c.Request.Method = "BAD METHOD\x00"
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://ark.cn-beijing.volces.com"},
	}
	got, err := a.DoRequest(c, info, nil)
	_ = got
	if err == nil || !strings.Contains(err.Error(), "new request failed") {
		t.Fatalf("expected 'new request failed' wrapping error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// DoResponse — context/parse error branches (hermetic, no network)
// ---------------------------------------------------------------------------

func TestDoResponse_AudioSpeechStream_MissingContextRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := newGinContext()
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeAudioSpeech,
		IsStream:  true,
	}
	usage, apiErr := a.DoResponse(c, &http.Response{}, info)
	if usage != nil {
		t.Errorf("expected nil usage, got %v", usage)
	}
	if apiErr == nil || !strings.Contains(apiErr.Err.Error(), "volcengine TTS request not found in context") {
		t.Fatalf("expected missing-context error, got %v", apiErr)
	}
}

func TestDoResponse_AudioSpeechStream_WrongContextType(t *testing.T) {
	a := &Adaptor{}
	c, _ := newGinContext()
	c.Set(contextKeyTTSRequest, "not-a-volcengine-request")
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeAudioSpeech,
		IsStream:  true,
	}
	usage, apiErr := a.DoResponse(c, &http.Response{}, info)
	if usage != nil {
		t.Errorf("expected nil usage, got %v", usage)
	}
	if apiErr == nil || !strings.Contains(apiErr.Err.Error(), "invalid volcengine TTS request type") {
		t.Fatalf("expected invalid-type error, got %v", apiErr)
	}
}

func TestDoResponse_AudioSpeechNonStream_BadJSON(t *testing.T) {
	a := &Adaptor{}
	c, _ := newGinContext()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeAudioSpeech}
	resp := &http.Response{Body: readCloserFrom("not json")}
	usage, apiErr := a.DoResponse(c, resp, info)
	if usage != nil {
		t.Errorf("expected nil usage, got %v", usage)
	}
	if apiErr == nil || !strings.Contains(apiErr.Err.Error(), "failed to parse volcengine response") {
		t.Fatalf("expected parse error, got %v", apiErr)
	}
}

func TestDoResponse_ClaudeFormatSpecialBase_RoutesToClaudeHandler(t *testing.T) {
	a := &Adaptor{}
	c, _ := newGinContext()
	resp := &http.Response{StatusCode: http.StatusOK, Body: readCloserFrom("not valid claude json")}
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "doubao-coding-plan"},
	}
	usage, apiErr := a.DoResponse(c, resp, info)
	_ = usage
	if apiErr == nil {
		t.Fatalf("expected error routing through claude.ClaudeHandler for malformed body")
	}
}

func TestDoResponse_DefaultFormat_RoutesToOpenAIAdaptor(t *testing.T) {
	a := &Adaptor{}
	c, _ := newGinContext()
	resp := &http.Response{StatusCode: http.StatusOK, Body: readCloserFrom("not json")}
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
	usage, apiErr := a.DoResponse(c, resp, info)
	_ = usage
	if apiErr == nil || apiErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected bad-response-body error from openai adaptor, got %v", apiErr)
	}
}

func TestDoResponse_AudioSpeechNonStream_Success(t *testing.T) {
	a := &Adaptor{}
	c, _ := newGinContext()
	audio := base64.StdEncoding.EncodeToString([]byte("PCMDATA"))
	body := `{"reqid":"r1","code":3000,"message":"ok","data":"` + audio + `"}`
	resp := &http.Response{Body: readCloserFrom(body)}
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeAudioSpeech}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Err)
	}
	if usage == nil {
		t.Fatalf("expected non-nil usage")
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage, got %T", usage)
	}
	if u.CompletionTokens != 0 {
		t.Errorf("CompletionTokens = %d, want 0", u.CompletionTokens)
	}
}

// ---------------------------------------------------------------------------
// handleTTSResponse (direct, private function)
// ---------------------------------------------------------------------------

func TestHandleTTSResponse(t *testing.T) {
	t.Run("non-3000 code returns bad response error", func(t *testing.T) {
		c, _ := newGinContext()
		body := `{"code":5000,"message":"boom"}`
		resp := &http.Response{Body: readCloserFrom(body)}
		usage, apiErr := handleTTSResponse(c, resp, &relaycommon.RelayInfo{}, "mp3")
		if usage != nil {
			t.Errorf("expected nil usage, got %v", usage)
		}
		if apiErr == nil || apiErr.Err.Error() != "boom" {
			t.Fatalf("expected error message 'boom', got %v", apiErr)
		}
		if apiErr.StatusCode != http.StatusBadRequest {
			t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusBadRequest)
		}
	})

	t.Run("invalid base64 data errors", func(t *testing.T) {
		c, _ := newGinContext()
		body := `{"code":3000,"message":"ok","data":"not-valid-base64!!"}`
		resp := &http.Response{Body: readCloserFrom(body)}
		usage, apiErr := handleTTSResponse(c, resp, &relaycommon.RelayInfo{}, "mp3")
		if usage != nil {
			t.Errorf("expected nil usage, got %v", usage)
		}
		if apiErr == nil || !strings.Contains(apiErr.Err.Error(), "failed to decode audio data") {
			t.Fatalf("expected decode error, got %v", apiErr)
		}
	})

	t.Run("success sets content type and writes audio body", func(t *testing.T) {
		c, w := newGinContext()
		audio := base64.StdEncoding.EncodeToString([]byte("RIFFDATA"))
		body := `{"code":3000,"message":"ok","data":"` + audio + `"}`
		resp := &http.Response{Body: readCloserFrom(body)}
		usage, apiErr := handleTTSResponse(c, resp, &relaycommon.RelayInfo{}, "wav")
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr.Err)
		}
		if usage == nil {
			t.Fatalf("expected non-nil usage")
		}
		if w.Header().Get("Content-Type") != "audio/wav" {
			t.Errorf("Content-Type = %q, want audio/wav", w.Header().Get("Content-Type"))
		}
		if w.Body.String() != "RIFFDATA" {
			t.Errorf("body = %q, want RIFFDATA", w.Body.String())
		}
	})
}

// ---------------------------------------------------------------------------
// handleTTSWebSocketResponse (direct, private function) — loopback websocket
// server only, no external network.
// ---------------------------------------------------------------------------

func TestHandleTTSWebSocketResponse_InvalidApiKey_NoDialAttempted(t *testing.T) {
	c, _ := newGinContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "no-pipe"}}
	usage, apiErr := handleTTSWebSocketResponse(c, "ws://127.0.0.1:1/unreachable", VolcengineTTSRequest{}, info, "mp3")
	if usage != nil {
		t.Errorf("expected nil usage, got %v", usage)
	}
	if apiErr == nil || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized error before any dial, got %v", apiErr)
	}
}

func TestHandleTTSWebSocketResponse_DialFailsOnNonWebsocketScheme(t *testing.T) {
	c, _ := newGinContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "app|tok"}}
	// http:// (not ws/wss) is rejected by the dialer before any network I/O.
	usage, apiErr := handleTTSWebSocketResponse(c, "http://127.0.0.1/x", VolcengineTTSRequest{}, info, "mp3")
	if usage != nil {
		t.Errorf("expected nil usage, got %v", usage)
	}
	if apiErr == nil || !strings.Contains(apiErr.Err.Error(), "failed to connect to websocket") {
		t.Fatalf("expected websocket connect error, got %v", apiErr)
	}
}

// wsTTSServer spins up a local (loopback-only) websocket endpoint driven by a
// scripted list of raw frames the "upstream" sends after receiving the
// client's FullClientRequest frame.
func wsTTSServer(t *testing.T, frames [][]byte, closeCode int) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// Drain the client's initial FullClientRequest frame.
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		for _, frame := range frames {
			if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
				return
			}
		}
		if closeCode != 0 {
			_ = conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(closeCode, ""), time.Now().Add(time.Second))
		}
	}))
	return srv
}

func marshalMsg(t *testing.T, msg *Message) []byte {
	t.Helper()
	b, err := msg.Marshal()
	if err != nil {
		t.Fatalf("failed to marshal scripted message: %v", err)
	}
	return b
}

func TestHandleTTSWebSocketResponse_FullLifecycle(t *testing.T) {
	frontend := &Message{MsgType: MsgTypeFrontEndResultServer, MsgTypeFlag: MsgTypeFlagNoSeq, Payload: []byte("meta")}
	audioChunk := &Message{MsgType: MsgTypeAudioOnlyServer, MsgTypeFlag: MsgTypeFlagPositiveSeq, Sequence: 1, Payload: []byte("chunk1")}
	audioFinal := &Message{MsgType: MsgTypeAudioOnlyServer, MsgTypeFlag: MsgTypeFlagPositiveSeq, Sequence: -1, Payload: []byte("chunkN")}

	frames := [][]byte{
		marshalMsg(t, frontend),
		marshalMsg(t, audioChunk),
		marshalMsg(t, audioFinal),
	}
	srv := wsTTSServer(t, frames, 0)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	c, w := newGinContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "app|tok"}}
	req := VolcengineTTSRequest{Request: VolcengineTTSReqInfo{ReqID: "r1", Text: "hi", Operation: "submit"}}

	usage, apiErr := handleTTSWebSocketResponse(c, wsURL, req, info, "mp3")
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Err)
	}
	if usage == nil {
		t.Fatalf("expected non-nil usage")
	}
	if w.Header().Get("Content-Type") != "audio/mpeg" {
		t.Errorf("Content-Type = %q, want audio/mpeg", w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Body.String(), "chunk1") {
		t.Errorf("expected audio chunk1 written to response body, got %q", w.Body.String())
	}
}

func TestHandleTTSWebSocketResponse_ServerSendsErrorMessage(t *testing.T) {
	errMsg := &Message{MsgType: MsgTypeError, MsgTypeFlag: MsgTypeFlagNoSeq, ErrorCode: 42, Payload: []byte("boom")}
	frames := [][]byte{marshalMsg(t, errMsg)}
	srv := wsTTSServer(t, frames, 0)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, _ := newGinContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "app|tok"}}
	req := VolcengineTTSRequest{Request: VolcengineTTSReqInfo{ReqID: "r1", Text: "hi"}}

	usage, apiErr := handleTTSWebSocketResponse(c, wsURL, req, info, "mp3")
	if usage != nil {
		t.Errorf("expected nil usage, got %v", usage)
	}
	if apiErr == nil || !strings.Contains(apiErr.Err.Error(), "received error from server") {
		t.Fatalf("expected server-error propagation, got %v", apiErr)
	}
}

func TestHandleTTSWebSocketResponse_NormalCloseEndsLoopGracefully(t *testing.T) {
	srv := wsTTSServer(t, nil, websocket.CloseNormalClosure)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, w := newGinContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "app|tok"}}
	req := VolcengineTTSRequest{Request: VolcengineTTSReqInfo{ReqID: "r1", Text: "hi"}}

	usage, apiErr := handleTTSWebSocketResponse(c, wsURL, req, info, "mp3")
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Err)
	}
	if usage == nil {
		t.Fatalf("expected non-nil usage after graceful close")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

// ---------------------------------------------------------------------------
// mapVoiceType / mapEncoding / getContentTypeByEncoding / parseVolcengineAuth
// / generateRequestID
// ---------------------------------------------------------------------------

func TestParseVolcengineAuth(t *testing.T) {
	tests := []struct {
		name    string
		apiKey  string
		wantApp string
		wantTok string
		wantErr bool
	}{
		{"valid", "myapp|mytoken", "myapp", "mytoken", false},
		{"no pipe", "noPipeHere", "", "", true},
		{"too many parts", "a|b|c", "", "", true},
		{"empty string", "", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, tok, err := parseVolcengineAuth(tt.apiKey)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if app != tt.wantApp || tok != tt.wantTok {
				t.Errorf("got (%q, %q), want (%q, %q)", app, tok, tt.wantApp, tt.wantTok)
			}
		})
	}
}

func TestMapVoiceType(t *testing.T) {
	tests := []struct{ in, want string }{
		{"alloy", "zh_male_M392_conversation_wvae_bigtts"},
		{"echo", "zh_male_wenhao_mars_bigtts"},
		{"fable", "zh_female_tianmei_mars_bigtts"},
		{"onyx", "zh_male_zhibei_mars_bigtts"},
		{"nova", "zh_female_shuangkuaisisi_mars_bigtts"},
		{"shimmer", "zh_female_cancan_mars_bigtts"},
		{"my-custom-voice", "my-custom-voice"},
	}
	for _, tt := range tests {
		if got := mapVoiceType(tt.in); got != tt.want {
			t.Errorf("mapVoiceType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMapEncoding(t *testing.T) {
	tests := []struct{ in, want string }{
		{"mp3", "mp3"},
		{"opus", "ogg_opus"},
		{"aac", "mp3"},
		{"flac", "mp3"},
		{"wav", "wav"},
		{"pcm", "pcm"},
		{"unknown-format", "mp3"},
		{"", "mp3"},
	}
	for _, tt := range tests {
		if got := mapEncoding(tt.in); got != tt.want {
			t.Errorf("mapEncoding(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGetContentTypeByEncoding(t *testing.T) {
	tests := []struct{ in, want string }{
		{"mp3", "audio/mpeg"},
		{"ogg_opus", "audio/ogg"},
		{"wav", "audio/wav"},
		{"pcm", "audio/pcm"},
		{"something-else", "application/octet-stream"},
	}
	for _, tt := range tests {
		if got := getContentTypeByEncoding(tt.in); got != tt.want {
			t.Errorf("getContentTypeByEncoding(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGenerateRequestID(t *testing.T) {
	id1 := generateRequestID()
	id2 := generateRequestID()
	if id1 == "" || id2 == "" {
		t.Fatalf("expected non-empty request IDs")
	}
	if id1 == id2 {
		t.Errorf("expected distinct request IDs across calls, got same value %q twice", id1)
	}
	if len(id1) != 36 {
		t.Errorf("expected 36-char UUID string, got %d chars: %q", len(id1), id1)
	}
}

// ---------------------------------------------------------------------------
// detectImageMimeType
// ---------------------------------------------------------------------------

func TestDetectImageMimeType(t *testing.T) {
	tests := []struct{ filename, want string }{
		{"photo.jpg", "image/jpeg"},
		{"photo.JPEG", "image/jpeg"},
		{"photo.png", "image/png"},
		{"photo.webp", "image/webp"},
		{"photo.jpx", "image/jpeg"},
		{"photo.gif", "image/png"},
		{"noext", "image/png"},
	}
	for _, tt := range tests {
		if got := detectImageMimeType(tt.filename); got != tt.want {
			t.Errorf("detectImageMimeType(%q) = %q, want %q", tt.filename, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// MsgType / EventType String()
// ---------------------------------------------------------------------------

func TestMsgTypeString(t *testing.T) {
	tests := []struct {
		mt   MsgType
		want string
	}{
		{MsgTypeFullClientRequest, "MsgType_FullClientRequest"},
		{MsgTypeAudioOnlyClient, "MsgType_AudioOnlyClient"},
		{MsgTypeFullServerResponse, "MsgType_FullServerResponse"},
		{MsgTypeAudioOnlyServer, "MsgType_AudioOnlyServer"},
		{MsgTypeError, "MsgType_Error"},
		{MsgTypeFrontEndResultServer, "MsgType_FrontEndResultServer"},
		{MsgType(0xE), "MsgType_(14)"},
	}
	for _, tt := range tests {
		if got := tt.mt.String(); got != tt.want {
			t.Errorf("MsgType(%d).String() = %q, want %q", tt.mt, got, tt.want)
		}
	}
}

func TestEventTypeString(t *testing.T) {
	tests := []struct {
		et   EventType
		want string
	}{
		{EventType_None, "EventType_None"},
		{EventType_StartConnection, "EventType_StartConnection"},
		{EventType_FinishConnection, "EventType_FinishConnection"},
		{EventType_ConnectionStarted, "EventType_ConnectionStarted"},
		{EventType_ConnectionFailed, "EventType_ConnectionFailed"},
		{EventType_ConnectionFinished, "EventType_ConnectionFinished"},
		{EventType_StartSession, "EventType_StartSession"},
		{EventType_CancelSession, "EventType_CancelSession"},
		{EventType_FinishSession, "EventType_FinishSession"},
		{EventType_SessionStarted, "EventType_SessionStarted"},
		{EventType_SessionCanceled, "EventType_SessionCanceled"},
		{EventType_SessionFinished, "EventType_SessionFinished"},
		{EventType_SessionFailed, "EventType_SessionFailed"},
		{EventType_UsageResponse, "EventType_UsageResponse"},
		{EventType_TaskRequest, "EventType_TaskRequest"},
		{EventType_UpdateConfig, "EventType_UpdateConfig"},
		{EventType_AudioMuted, "EventType_AudioMuted"},
		{EventType_SayHello, "EventType_SayHello"},
		{EventType_TTSSentenceStart, "EventType_TTSSentenceStart"},
		{EventType_TTSSentenceEnd, "EventType_TTSSentenceEnd"},
		{EventType_TTSResponse, "EventType_TTSResponse"},
		{EventType_TTSEnded, "EventType_TTSEnded"},
		{EventType_PodcastRoundStart, "EventType_PodcastRoundStart"},
		{EventType_PodcastRoundResponse, "EventType_PodcastRoundResponse"},
		{EventType_PodcastRoundEnd, "EventType_PodcastRoundEnd"},
		{EventType_ASRInfo, "EventType_ASRInfo"},
		{EventType_ASRResponse, "EventType_ASRResponse"},
		{EventType_ASREnded, "EventType_ASREnded"},
		{EventType_ChatTTSText, "EventType_ChatTTSText"},
		{EventType_ChatResponse, "EventType_ChatResponse"},
		{EventType_ChatEnded, "EventType_ChatEnded"},
		{EventType_SourceSubtitleStart, "EventType_SourceSubtitleStart"},
		{EventType_SourceSubtitleResponse, "EventType_SourceSubtitleResponse"},
		{EventType_SourceSubtitleEnd, "EventType_SourceSubtitleEnd"},
		{EventType_TranslationSubtitleStart, "EventType_TranslationSubtitleStart"},
		{EventType_TranslationSubtitleResponse, "EventType_TranslationSubtitleResponse"},
		{EventType_TranslationSubtitleEnd, "EventType_TranslationSubtitleEnd"},
		{EventType(99999), "EventType_(99999)"},
	}
	for _, tt := range tests {
		if got := tt.et.String(); got != tt.want {
			t.Errorf("EventType(%d).String() = %q, want %q", tt.et, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Message Marshal/Unmarshal round trips + error paths
// ---------------------------------------------------------------------------

func TestMessage_RoundTrip_FullClientRequest_NoSeq(t *testing.T) {
	msg, err := NewMessage(MsgTypeFullClientRequest, MsgTypeFlagNoSeq)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	msg.Payload = []byte(`{"hello":"world"}`)

	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got, err := NewMessageFromBytes(data)
	if err != nil {
		t.Fatalf("NewMessageFromBytes: %v", err)
	}
	if string(got.Payload) != `{"hello":"world"}` {
		t.Errorf("Payload = %q, want original json", got.Payload)
	}
	if got.MsgType != MsgTypeFullClientRequest {
		t.Errorf("MsgType = %v, want %v", got.MsgType, MsgTypeFullClientRequest)
	}
}

func TestMessage_RoundTrip_AudioOnlyServer_WithSequence(t *testing.T) {
	msg, err := NewMessage(MsgTypeAudioOnlyServer, MsgTypeFlagPositiveSeq)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	msg.Sequence = 7
	msg.Payload = []byte("binarychunk")

	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := NewMessageFromBytes(data)
	if err != nil {
		t.Fatalf("NewMessageFromBytes: %v", err)
	}
	if got.Sequence != 7 {
		t.Errorf("Sequence = %d, want 7", got.Sequence)
	}
	if string(got.Payload) != "binarychunk" {
		t.Errorf("Payload = %q, want binarychunk", got.Payload)
	}
}

func TestMessage_RoundTrip_Error(t *testing.T) {
	msg, err := NewMessage(MsgTypeError, MsgTypeFlagNoSeq)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	msg.ErrorCode = 12345
	msg.Payload = []byte("error detail")

	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := NewMessageFromBytes(data)
	if err != nil {
		t.Fatalf("NewMessageFromBytes: %v", err)
	}
	if got.ErrorCode != 12345 {
		t.Errorf("ErrorCode = %d, want 12345", got.ErrorCode)
	}
}

func TestMessage_RoundTrip_WithEventAndSession(t *testing.T) {
	msg, err := NewMessage(MsgTypeFullClientRequest, MsgTypeFlagWithEvent)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	msg.EventType = EventType_StartSession
	msg.SessionID = "session-abc"
	msg.Payload = []byte("payload-with-event")

	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := NewMessageFromBytes(data)
	if err != nil {
		t.Fatalf("NewMessageFromBytes: %v", err)
	}
	if got.EventType != EventType_StartSession {
		t.Errorf("EventType = %v, want %v", got.EventType, EventType_StartSession)
	}
	if got.SessionID != "session-abc" {
		t.Errorf("SessionID = %q, want session-abc", got.SessionID)
	}
}

func TestMessage_RoundTrip_WithEvent_SkipsSessionIDForConnectionLifecycleEvents(t *testing.T) {
	// EventType_StartConnection is in both writeSessionID's and
	// readSessionID's "skip" event sets, and NOT in readConnectID's
	// read-trigger set, so this round-trips cleanly without any
	// session/connect bytes on the wire.
	msg, err := NewMessage(MsgTypeFullClientRequest, MsgTypeFlagWithEvent)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	msg.EventType = EventType_StartConnection
	msg.SessionID = "should-not-be-written"
	msg.Payload = []byte("hello")

	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := NewMessageFromBytes(data)
	if err != nil {
		t.Fatalf("NewMessageFromBytes: %v", err)
	}
	if got.SessionID != "" {
		t.Errorf("SessionID = %q, want empty (writeSessionID skips StartConnection)", got.SessionID)
	}
	if string(got.Payload) != "hello" {
		t.Errorf("Payload = %q, want hello", got.Payload)
	}
}

func TestMessage_RoundTrip_WithEvent_EmptySessionIDStillReadable(t *testing.T) {
	msg, err := NewMessage(MsgTypeFullClientRequest, MsgTypeFlagWithEvent)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	msg.EventType = EventType_UpdateConfig // not in either skip set
	msg.SessionID = ""
	msg.Payload = []byte("cfg")

	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := NewMessageFromBytes(data)
	if err != nil {
		t.Fatalf("NewMessageFromBytes: %v", err)
	}
	if got.SessionID != "" {
		t.Errorf("SessionID = %q, want empty string round trip", got.SessionID)
	}
	if string(got.Payload) != "cfg" {
		t.Errorf("Payload = %q, want cfg", got.Payload)
	}
}

// TestMessage_Unmarshal_ConnectIDReadBranch hand-crafts a wire frame to
// exercise readConnectID's actual read branch. This branch has no matching
// writer in Marshal's writers() (writeConnectID does not exist), so it can
// only be reached via manually constructed bytes, not a Marshal round trip.
func TestMessage_Unmarshal_ConnectIDReadBranch(t *testing.T) {
	var buf bytes.Buffer
	// header: version=1,headerSize=1 ; msgType=FullServerResponse,flag=WithEvent ; serialization=JSON,compression=none
	buf.WriteByte(uint8(Version1)<<4 | uint8(HeaderSize4))
	buf.WriteByte(uint8(MsgTypeFullServerResponse)<<4 | uint8(MsgTypeFlagWithEvent))
	buf.WriteByte(uint8(SerializationJSON)<<4 | uint8(CompressionNone))
	buf.WriteByte(0) // header padding to reach headerSize*4 = 4 bytes total

	writeU32 := func(v uint32) {
		buf.WriteByte(byte(v >> 24))
		buf.WriteByte(byte(v >> 16))
		buf.WriteByte(byte(v >> 8))
		buf.WriteByte(byte(v))
	}

	writeU32(uint32(EventType_ConnectionStarted)) // readEvent (4 bytes)
	// readSessionID: EventType_ConnectionStarted is in its skip set -> no bytes.
	connID := "cid-123"
	writeU32(uint32(len(connID))) // readConnectID size
	buf.WriteString(connID)       // readConnectID bytes
	payload := "pl"
	writeU32(uint32(len(payload))) // readPayload size
	buf.WriteString(payload)

	got, err := NewMessageFromBytes(buf.Bytes())
	if err != nil {
		t.Fatalf("NewMessageFromBytes: %v", err)
	}
	if got.ConnectID != connID {
		t.Errorf("ConnectID = %q, want %q", got.ConnectID, connID)
	}
	if string(got.Payload) != payload {
		t.Errorf("Payload = %q, want %q", got.Payload, payload)
	}
}

func TestMessage_Unmarshal_InsufficientHeaderBytesErrors(t *testing.T) {
	// headerSize=2 => 8 header bytes expected, but only 3 bytes total supplied.
	data := []byte{
		uint8(Version1)<<4 | 2,
		uint8(MsgTypeFullClientRequest)<<4 | uint8(MsgTypeFlagNoSeq),
		uint8(SerializationJSON)<<4 | uint8(CompressionNone),
	}
	fresh, err := NewMessage(MsgTypeFullClientRequest, MsgTypeFlagNoSeq)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	err = fresh.Unmarshal(data)
	if err == nil || !strings.Contains(err.Error(), "insufficient header bytes") {
		t.Fatalf("expected 'insufficient header bytes' error, got %v", err)
	}
}

func TestNewMessageFromBytes_UnsupportedMsgTypeInReaders(t *testing.T) {
	// Top nibble 0 has no MsgType case in readers()'s switch.
	data := []byte{
		uint8(Version1)<<4 | uint8(HeaderSize4),
		0<<4 | uint8(MsgTypeFlagNoSeq),
		uint8(SerializationJSON) << 4,
		0,
	}
	_, err := NewMessageFromBytes(data)
	if err == nil || !strings.Contains(err.Error(), "unsupported message type") {
		t.Fatalf("expected 'unsupported message type' error, got %v", err)
	}
}

func TestNewMessageFromBytes_TooShort(t *testing.T) {
	_, err := NewMessageFromBytes([]byte{0x11, 0x12})
	if err == nil || !strings.Contains(err.Error(), "data too short") {
		t.Fatalf("expected 'data too short' error, got %v", err)
	}
}

func TestMessage_Marshal_UnsupportedMsgType(t *testing.T) {
	msg, err := NewMessage(MsgType(0), MsgTypeFlagNoSeq)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	_, err = msg.Marshal()
	if err == nil || !strings.Contains(err.Error(), "unsupported message type") {
		t.Fatalf("expected unsupported message type error, got %v", err)
	}
}

func TestMessage_Unmarshal_TrailingDataErrors(t *testing.T) {
	msg, err := NewMessage(MsgTypeFullClientRequest, MsgTypeFlagNoSeq)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	msg.Payload = []byte("x")
	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	data = append(data, 0xFF) // trailing garbage byte after a well-formed message.

	// NewMessageFromBytes derives MsgType/flag from the header before
	// delegating to Unmarshal, matching how production code always invokes it.
	_, err = NewMessageFromBytes(data)
	if err == nil || !strings.Contains(err.Error(), "unexpected data after message") {
		t.Fatalf("expected 'unexpected data after message' error, got %v", err)
	}
}

func TestMessage_String(t *testing.T) {
	audio := &Message{MsgType: MsgTypeAudioOnlyServer, MsgTypeFlag: MsgTypeFlagPositiveSeq, EventType: EventType_TTSResponse, Sequence: 3, Payload: []byte("abc")}
	if got := audio.String(); !strings.Contains(got, "Sequence: 3") {
		t.Errorf("String() = %q, want it to contain Sequence: 3", got)
	}

	errMsg := &Message{MsgType: MsgTypeError, EventType: EventType_None, ErrorCode: 9, Payload: []byte("bad")}
	if got := errMsg.String(); !strings.Contains(got, "ErrorCode: 9") {
		t.Errorf("String() = %q, want it to contain ErrorCode: 9", got)
	}

	plain := &Message{MsgType: MsgTypeFullClientRequest, EventType: EventType_None, Payload: []byte("plain")}
	if got := plain.String(); !strings.Contains(got, "Payload: plain") {
		t.Errorf("String() = %q, want it to contain Payload: plain", got)
	}

	audioNoSeq := &Message{MsgType: MsgTypeAudioOnlyClient, MsgTypeFlag: MsgTypeFlagNoSeq, EventType: EventType_None, Payload: []byte("abcd")}
	if got := audioNoSeq.String(); !strings.Contains(got, "PayloadSize: 4") || strings.Contains(got, "Sequence") {
		t.Errorf("String() = %q, want PayloadSize without Sequence for no-seq audio message", got)
	}

	withSeq := &Message{MsgType: MsgTypeFullClientRequest, MsgTypeFlag: MsgTypeFlagPositiveSeq, EventType: EventType_None, Sequence: 2, Payload: []byte("s")}
	if got := withSeq.String(); !strings.Contains(got, "Sequence: 2, Payload: s") {
		t.Errorf("String() = %q, want it to contain Sequence for non-audio message with seq flag", got)
	}
}

// ---------------------------------------------------------------------------
// FullClientRequest / ReceiveMessage — loopback websocket only.
// ---------------------------------------------------------------------------

func TestFullClientRequest_And_ReceiveMessage_RoundTrip(t *testing.T) {
	received := make(chan *Message, 1)
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		msg, err := ReceiveMessage(conn)
		if err != nil {
			return
		}
		received <- msg
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	payload := []byte(`{"req":"full-client"}`)
	if err := FullClientRequest(conn, payload); err != nil {
		t.Fatalf("FullClientRequest: %v", err)
	}

	select {
	case msg := <-received:
		if string(msg.Payload) != string(payload) {
			t.Errorf("received payload = %q, want %q", msg.Payload, payload)
		}
		if msg.MsgType != MsgTypeFullClientRequest {
			t.Errorf("received MsgType = %v, want %v", msg.MsgType, MsgTypeFullClientRequest)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for server to receive message")
	}
}

func TestFullClientRequest_ErrorsOnClosedConnection(t *testing.T) {
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conn.Close()
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.Close() // close the client side too, so WriteMessage fails immediately.

	if err := FullClientRequest(conn, []byte("payload")); err == nil {
		t.Fatalf("expected WriteMessage error on closed connection")
	}
}

func TestReceiveMessage_UnexpectedFrameTooShortErrors(t *testing.T) {
	errCh := make(chan error, 1)
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// Send a too-short binary frame; the client's ReceiveMessage should
		// surface NewMessageFromBytes's "data too short" error.
		_ = conn.WriteMessage(websocket.BinaryMessage, []byte{0x01, 0x02})
		time.Sleep(50 * time.Millisecond)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	go func() {
		_, err := ReceiveMessage(conn)
		errCh <- err
	}()

	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "data too short") {
			t.Fatalf("expected 'data too short' error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for ReceiveMessage")
	}
}

// Keep bytes/gin imports honest even if a future edit trims a call site above.
var _ = bytes.NewReader
var _ = gin.MIMEJSON
var _ = channelconstant.ChannelTypeVolcEngine
