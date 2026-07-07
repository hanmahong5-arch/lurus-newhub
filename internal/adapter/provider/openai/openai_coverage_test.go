package openai

import (
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// GetRequestURL
// ---------------------------------------------------------------------------

func TestAdaptor_GetRequestURL(t *testing.T) {
	t.Run("azure default api version and dot removal", func(t *testing.T) {
		a := &Adaptor{ChannelType: constant.ChannelTypeAzure}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:       constant.ChannelTypeAzure,
				ChannelBaseUrl:    "https://my-azure.openai.azure.com",
				ChannelCreateTime: 0, // before AzureNoRemoveDotTime -> dots removed
				UpstreamModelName: "gpt-3.5.turbo",
			},
			RequestURLPath: "/v1/chat/completions",
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		wantURL := "https://my-azure.openai.azure.com/openai/deployments/gpt-35turbo/chat/completions?api-version=" + constant.AzureDefaultAPIVersion
		if url != wantURL {
			t.Errorf("url = %q, want %q", url, wantURL)
		}
	})

	t.Run("azure explicit api version, no dot removal for recent channel", func(t *testing.T) {
		a := &Adaptor{ChannelType: constant.ChannelTypeAzure}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:       constant.ChannelTypeAzure,
				ChannelBaseUrl:    "https://my-azure.openai.azure.com",
				ApiVersion:        "2024-05-01",
				ChannelCreateTime: constant.AzureNoRemoveDotTime + 1,
				UpstreamModelName: "gpt-3.5.turbo",
			},
			RequestURLPath: "/v1/chat/completions",
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		wantURL := "https://my-azure.openai.azure.com/openai/deployments/gpt-3.5.turbo/chat/completions?api-version=2024-05-01"
		if url != wantURL {
			t.Errorf("url = %q, want %q", url, wantURL)
		}
	})

	t.Run("azure claude relay format rewrites task to chat/completions", func(t *testing.T) {
		a := &Adaptor{ChannelType: constant.ChannelTypeAzure}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:       constant.ChannelTypeAzure,
				ChannelBaseUrl:    "https://my-azure.openai.azure.com",
				ApiVersion:        "2024-05-01",
				ChannelCreateTime: constant.AzureNoRemoveDotTime + 1,
				UpstreamModelName: "gpt-4",
			},
			RequestURLPath: "/v1/messages",
			RelayFormat:    types.RelayFormatClaude,
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		wantURL := "https://my-azure.openai.azure.com/openai/deployments/gpt-4/chat/completions?api-version=2024-05-01"
		if url != wantURL {
			t.Errorf("url = %q, want %q", url, wantURL)
		}
	})

	t.Run("azure responses mode default subpath", func(t *testing.T) {
		a := &Adaptor{ChannelType: constant.ChannelTypeAzure}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:    constant.ChannelTypeAzure,
				ChannelBaseUrl: "https://my-azure.openai.azure.com",
			},
			RequestURLPath: "/v1/responses",
			RelayMode:      relayconstant.RelayModeResponses,
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://my-azure.openai.azure.com/openai/v1/responses?api-version=preview"
		if url != want {
			t.Errorf("url = %q, want %q", url, want)
		}
	})

	t.Run("azure responses mode cognitiveservices subpath", func(t *testing.T) {
		a := &Adaptor{ChannelType: constant.ChannelTypeAzure}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:    constant.ChannelTypeAzure,
				ChannelBaseUrl: "https://my-azure.cognitiveservices.azure.com",
				ApiVersion:     "2024-08-01",
			},
			RequestURLPath: "/v1/responses",
			RelayMode:      relayconstant.RelayModeResponses,
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://my-azure.cognitiveservices.azure.com/openai/responses?api-version=2024-08-01"
		if url != want {
			t.Errorf("url = %q, want %q", url, want)
		}
	})

	t.Run("azure responses mode explicit override version", func(t *testing.T) {
		a := &Adaptor{ChannelType: constant.ChannelTypeAzure}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:          constant.ChannelTypeAzure,
				ChannelBaseUrl:       "https://my-azure.openai.azure.com",
				ChannelOtherSettings: dto.ChannelOtherSettings{AzureResponsesVersion: "2025-custom"},
			},
			RequestURLPath: "/v1/responses",
			RelayMode:      relayconstant.RelayModeResponses,
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://my-azure.openai.azure.com/openai/v1/responses?api-version=2025-custom"
		if url != want {
			t.Errorf("url = %q, want %q", url, want)
		}
	})

	t.Run("azure realtime mode builds deployment url and rewrites base to wss", func(t *testing.T) {
		a := &Adaptor{ChannelType: constant.ChannelTypeAzure}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:       constant.ChannelTypeAzure,
				ChannelBaseUrl:    "https://my-azure.openai.azure.com",
				ApiVersion:        "2024-05-01",
				UpstreamModelName: "gpt-4o-realtime",
			},
			RequestURLPath: "/v1/realtime",
			RelayMode:      relayconstant.RelayModeRealtime,
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "wss://my-azure.openai.azure.com/openai/realtime?deployment=gpt-4o-realtime&api-version=2024-05-01"
		if url != want {
			t.Errorf("url = %q, want %q", url, want)
		}
		if info.ChannelBaseUrl != "wss://my-azure.openai.azure.com" {
			t.Errorf("ChannelBaseUrl = %q, want wss:// rewrite", info.ChannelBaseUrl)
		}
	})

	t.Run("realtime mode http base rewritten to ws for non-azure", func(t *testing.T) {
		a := &Adaptor{ChannelType: constant.ChannelTypeOpenAI}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:    constant.ChannelTypeOpenAI,
				ChannelBaseUrl: "http://localhost:8080",
			},
			RequestURLPath: "/v1/realtime",
			RelayMode:      relayconstant.RelayModeRealtime,
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "ws://localhost:8080/v1/realtime"
		if url != want {
			t.Errorf("url = %q, want %q", url, want)
		}
	})

	t.Run("custom channel replaces model placeholder", func(t *testing.T) {
		a := &Adaptor{ChannelType: constant.ChannelTypeCustom}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:       constant.ChannelTypeCustom,
				ChannelBaseUrl:    "https://api.example.com/{model}/v1",
				UpstreamModelName: "gpt-4o",
			},
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://api.example.com/gpt-4o/v1"
		if url != want {
			t.Errorf("url = %q, want %q", url, want)
		}
	})

	t.Run("default channel with claude relay format", func(t *testing.T) {
		a := &Adaptor{ChannelType: constant.ChannelTypeOpenAI}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:    constant.ChannelTypeOpenAI,
				ChannelBaseUrl: "https://api.openai.com",
			},
			RelayFormat: types.RelayFormatClaude,
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://api.openai.com/v1/chat/completions"
		if url != want {
			t.Errorf("url = %q, want %q", url, want)
		}
	})

	t.Run("default channel with gemini relay format", func(t *testing.T) {
		a := &Adaptor{ChannelType: constant.ChannelTypeOpenAI}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:    constant.ChannelTypeOpenAI,
				ChannelBaseUrl: "https://api.openai.com",
			},
			RelayFormat: types.RelayFormatGemini,
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://api.openai.com/v1/chat/completions"
		if url != want {
			t.Errorf("url = %q, want %q", url, want)
		}
	})

	t.Run("default channel plain openai passthrough", func(t *testing.T) {
		a := &Adaptor{ChannelType: constant.ChannelTypeOpenAI}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:    constant.ChannelTypeOpenAI,
				ChannelBaseUrl: "https://api.openai.com",
			},
			RequestURLPath: "/v1/chat/completions",
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://api.openai.com/v1/chat/completions"
		if url != want {
			t.Errorf("url = %q, want %q", url, want)
		}
	})
}

// ---------------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------------

func newTestGinContext(method, target string, body *strings.Reader) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, target, body)
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	c.Request = req
	return c, w
}

func TestAdaptor_SetupRequestHeader(t *testing.T) {
	t.Run("azure sets api-key and returns early", func(t *testing.T) {
		c, _ := newTestGinContext(http.MethodPost, "/v1/chat/completions", nil)
		a := &Adaptor{ChannelType: constant.ChannelTypeAzure}
		header := &http.Header{}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeAzure, ApiKey: "sk-azure"},
		}
		err := a.SetupRequestHeader(c, header, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if header.Get("api-key") != "sk-azure" {
			t.Errorf("api-key = %q, want sk-azure", header.Get("api-key"))
		}
		if header.Get("Authorization") != "" {
			t.Errorf("Authorization should not be set for azure, got %q", header.Get("Authorization"))
		}
	})

	t.Run("openai sets bearer auth and organization", func(t *testing.T) {
		c, _ := newTestGinContext(http.MethodPost, "/v1/chat/completions", nil)
		a := &Adaptor{ChannelType: constant.ChannelTypeOpenAI}
		header := &http.Header{}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, ApiKey: "sk-oai", Organization: "org-1"},
		}
		err := a.SetupRequestHeader(c, header, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if header.Get("Authorization") != "Bearer sk-oai" {
			t.Errorf("Authorization = %q, want Bearer sk-oai", header.Get("Authorization"))
		}
		if header.Get("OpenAI-Organization") != "org-1" {
			t.Errorf("OpenAI-Organization = %q, want org-1", header.Get("OpenAI-Organization"))
		}
	})

	t.Run("openrouter sets referer and title headers", func(t *testing.T) {
		c, _ := newTestGinContext(http.MethodPost, "/v1/chat/completions", nil)
		a := &Adaptor{ChannelType: constant.ChannelTypeOpenRouter}
		header := &http.Header{}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenRouter, ApiKey: "sk-or"},
		}
		err := a.SetupRequestHeader(c, header, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if header.Get("HTTP-Referer") != "https://www.newapi.ai" {
			t.Errorf("HTTP-Referer = %q", header.Get("HTTP-Referer"))
		}
		if header.Get("X-Title") != "New API" {
			t.Errorf("X-Title = %q", header.Get("X-Title"))
		}
	})

	t.Run("realtime mode without Sec-WebSocket-Protocol sets openai-beta header", func(t *testing.T) {
		c, _ := newTestGinContext(http.MethodGet, "/v1/realtime", nil)
		a := &Adaptor{ChannelType: constant.ChannelTypeOpenAI}
		header := &http.Header{}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, ApiKey: "sk-rt"},
			RelayMode:   relayconstant.RelayModeRealtime,
		}
		err := a.SetupRequestHeader(c, header, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if header.Get("openai-beta") != "realtime=v1" {
			t.Errorf("openai-beta = %q, want realtime=v1", header.Get("openai-beta"))
		}
		if header.Get("Authorization") != "Bearer sk-rt" {
			t.Errorf("Authorization = %q, want Bearer sk-rt", header.Get("Authorization"))
		}
	})

	t.Run("realtime mode with Sec-WebSocket-Protocol builds protocol list", func(t *testing.T) {
		c, _ := newTestGinContext(http.MethodGet, "/v1/realtime", nil)
		c.Request.Header.Set("Sec-WebSocket-Protocol", "realtime")
		a := &Adaptor{ChannelType: constant.ChannelTypeOpenAI}
		header := &http.Header{}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, ApiKey: "sk-rt2"},
			RelayMode:   relayconstant.RelayModeRealtime,
		}
		err := a.SetupRequestHeader(c, header, info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := header.Get("Sec-WebSocket-Protocol")
		want := "realtime,openai-insecure-api-key.sk-rt2,openai-beta.realtime-v1"
		if got != want {
			t.Errorf("Sec-WebSocket-Protocol = %q, want %q", got, want)
		}
	})
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName branches
// ---------------------------------------------------------------------------

func TestAdaptor_GetModelList_Branches(t *testing.T) {
	tests := []struct {
		name         string
		channelType  int
		wantNonEmpty bool
	}{
		{"360", constant.ChannelType360, true},
		{"lingyiwanwu", constant.ChannelTypeLingYiWanWu, true},
		{"xinference", constant.ChannelTypeXinference, true},
		{"openrouter", constant.ChannelTypeOpenRouter, false}, // openrouter.ModelList is intentionally empty
		{"default openai", constant.ChannelTypeOpenAI, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Adaptor{ChannelType: tt.channelType}
			list := a.GetModelList()
			if tt.wantNonEmpty && len(list) == 0 {
				t.Errorf("GetModelList() for %s should be non-empty", tt.name)
			}
			if !tt.wantNonEmpty && list == nil {
				t.Errorf("GetModelList() for %s should be an initialized (possibly empty) slice, got nil", tt.name)
			}
		})
	}
}

func TestAdaptor_GetChannelName_Branches(t *testing.T) {
	tests := []struct {
		name        string
		channelType int
		want        string
	}{
		{"360", constant.ChannelType360, "ai360"},
		{"lingyiwanwu", constant.ChannelTypeLingYiWanWu, "lingyiwanwu"},
		{"xinference", constant.ChannelTypeXinference, "xinference"},
		{"openrouter", constant.ChannelTypeOpenRouter, "openrouter"},
		{"default openai", constant.ChannelTypeOpenAI, "openai"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Adaptor{ChannelType: tt.channelType}
			got := a.GetChannelName()
			if got != tt.want {
				t.Errorf("GetChannelName() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ConvertRerankRequest / ConvertEmbeddingRequest / ConvertOpenAIResponsesRequest
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertRerankRequest(t *testing.T) {
	c, _ := newTestGinContext(http.MethodPost, "/v1/rerank", nil)
	a := &Adaptor{}
	req := dto.RerankRequest{Query: "q", Model: "rerank-1", Documents: []any{"a", "b"}}
	result, err := a.ConvertRerankRequest(c, relayconstant.RelayModeRerank, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r, ok := result.(dto.RerankRequest)
	if !ok {
		t.Fatalf("result type = %T, want dto.RerankRequest", result)
	}
	if r.Query != "q" || r.Model != "rerank-1" {
		t.Errorf("result = %+v, want passthrough of input", r)
	}
}

func TestAdaptor_ConvertEmbeddingRequest(t *testing.T) {
	c, _ := newTestGinContext(http.MethodPost, "/v1/embeddings", nil)
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{}
	req := dto.EmbeddingRequest{Model: "text-embedding-3-small", Input: "hello"}
	result, err := a.ConvertEmbeddingRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r, ok := result.(dto.EmbeddingRequest)
	if !ok {
		t.Fatalf("result type = %T, want dto.EmbeddingRequest", result)
	}
	if r.Model != "text-embedding-3-small" {
		t.Errorf("Model = %q, want passthrough", r.Model)
	}
}

func TestAdaptor_ConvertOpenAIResponsesRequest(t *testing.T) {
	c, _ := newTestGinContext(http.MethodPost, "/v1/responses", nil)
	a := &Adaptor{}

	t.Run("suffix extraction with nil reasoning", func(t *testing.T) {
		info := &relaycommon.RelayInfo{}
		req := dto.OpenAIResponsesRequest{Model: "o3-mini-high"}
		result, err := a.ConvertOpenAIResponsesRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		r := result.(dto.OpenAIResponsesRequest)
		if r.Model != "o3-mini" {
			t.Errorf("Model = %q, want o3-mini", r.Model)
		}
		if r.Reasoning == nil || r.Reasoning.Effort != "high" {
			t.Errorf("Reasoning = %+v, want effort=high", r.Reasoning)
		}
	})

	t.Run("suffix extraction with existing reasoning", func(t *testing.T) {
		info := &relaycommon.RelayInfo{}
		req := dto.OpenAIResponsesRequest{Model: "o3-mini-low", Reasoning: &dto.Reasoning{Summary: "keep-me"}}
		result, err := a.ConvertOpenAIResponsesRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		r := result.(dto.OpenAIResponsesRequest)
		if r.Reasoning.Effort != "low" {
			t.Errorf("Effort = %q, want low", r.Reasoning.Effort)
		}
		if r.Reasoning.Summary != "keep-me" {
			t.Errorf("Summary = %q, want preserved keep-me", r.Reasoning.Summary)
		}
	})

	t.Run("no suffix passthrough", func(t *testing.T) {
		info := &relaycommon.RelayInfo{}
		req := dto.OpenAIResponsesRequest{Model: "gpt-4o"}
		result, err := a.ConvertOpenAIResponsesRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		r := result.(dto.OpenAIResponsesRequest)
		if r.Model != "gpt-4o" {
			t.Errorf("Model = %q, want gpt-4o", r.Model)
		}
		if r.Reasoning != nil {
			t.Errorf("Reasoning = %+v, want nil", r.Reasoning)
		}
	})
}

// ---------------------------------------------------------------------------
// ConvertImageRequest
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertImageRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("default relay mode passthrough", func(t *testing.T) {
		c, _ := newTestGinContext(http.MethodPost, "/v1/images/generations", nil)
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesGenerations}
		req := dto.ImageRequest{Model: "dall-e-3", Prompt: "a cat"}
		result, err := a.ConvertImageRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		r, ok := result.(dto.ImageRequest)
		if !ok {
			t.Fatalf("result type = %T", result)
		}
		if r.Prompt != "a cat" {
			t.Errorf("Prompt = %q, want passthrough", r.Prompt)
		}
	})

	t.Run("images edits without multipart form data errors", func(t *testing.T) {
		c, _ := newTestGinContext(http.MethodPost, "/v1/images/edits", strings.NewReader("not multipart"))
		c.Request.Header.Set("Content-Type", "application/json")
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesEdits}
		req := dto.ImageRequest{Model: "gpt-image-1", Prompt: "edit me"}
		_, err := a.ConvertImageRequest(c, info, req)
		if err == nil {
			t.Fatal("expected error when multipart form cannot be parsed")
		}
	})

	t.Run("images edits missing image field errors", func(t *testing.T) {
		var body strings.Builder
		mw := multipart.NewWriter(&stringsWriterWrapper{&body})
		_ = mw.WriteField("model", "gpt-image-1")
		_ = mw.Close()

		c, _ := newTestGinContext(http.MethodPost, "/v1/images/edits", strings.NewReader(body.String()))
		c.Request.Header.Set("Content-Type", mw.FormDataContentType())
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesEdits}
		req := dto.ImageRequest{Model: "gpt-image-1", Prompt: "edit me"}
		_, err := a.ConvertImageRequest(c, info, req)
		if err == nil {
			t.Fatal("expected error for missing image field")
		}
		if !strings.Contains(err.Error(), "image is required") {
			t.Errorf("error = %q, want contains 'image is required'", err.Error())
		}
	})
}

// stringsWriterWrapper adapts a *strings.Builder to io.Writer for multipart.NewWriter use above.
type stringsWriterWrapper struct {
	b *strings.Builder
}

func (s *stringsWriterWrapper) Write(p []byte) (int, error) {
	return s.b.Write(p)
}

// ---------------------------------------------------------------------------
// ConvertAudioRequest
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertAudioRequest_Speech(t *testing.T) {
	c, _ := newTestGinContext(http.MethodPost, "/v1/audio/speech", nil)
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeAudioSpeech}
	req := dto.AudioRequest{Model: "tts-1", Input: "hello world", Voice: "alloy"}
	reader, err := a.ConvertAudioRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reader == nil {
		t.Fatal("expected non-nil reader")
	}
	if a.ResponseFormat != req.ResponseFormat {
		t.Errorf("ResponseFormat = %q, want %q", a.ResponseFormat, req.ResponseFormat)
	}
	buf := make([]byte, 512)
	n, _ := reader.Read(buf)
	body := string(buf[:n])
	if !strings.Contains(body, "hello world") || !strings.Contains(body, "tts-1") {
		t.Errorf("marshalled body = %q, want to contain model+input", body)
	}
}

// ---------------------------------------------------------------------------
// detectImageMimeType
// ---------------------------------------------------------------------------

func TestDetectImageMimeType(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"photo.jpg", "image/jpeg"},
		{"photo.JPEG", "image/jpeg"},
		{"pic.png", "image/png"},
		{"pic.PNG", "image/png"},
		{"pic.webp", "image/webp"},
		{"pic.jpx", "image/jpeg"}, // ".jp" prefix fallback
		{"pic.unknown", "image/png"},
		{"noext", "image/png"},
	}
	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := detectImageMimeType(tt.filename)
			if got != tt.want {
				t.Errorf("detectImageMimeType(%q) = %q, want %q", tt.filename, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ConvertGeminiRequest / ConvertClaudeRequest (smoke — delegate to app.*ToOpenAIRequest)
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertGeminiRequest(t *testing.T) {
	c, _ := newTestGinContext(http.MethodPost, "/v1beta/models/gpt-4o:generateContent", nil)
	a := &Adaptor{ChannelType: constant.ChannelTypeOpenAI}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-4o"},
	}
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{{Text: "hello"}}},
		},
	}
	result, err := a.ConvertGeminiRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	r, ok := result.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("result type = %T, want *dto.GeneralOpenAIRequest", result)
	}
	if len(r.Messages) == 0 {
		t.Error("expected converted messages to be non-empty")
	}
}

func TestAdaptor_ConvertClaudeRequest(t *testing.T) {
	c, _ := newTestGinContext(http.MethodPost, "/v1/messages", nil)
	a := &Adaptor{ChannelType: constant.ChannelTypeOpenAI}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-4o"},
	}
	req := &dto.ClaudeRequest{
		Model:     "gpt-4o",
		MaxTokens: 100,
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hello"},
		},
	}
	result, err := a.ConvertClaudeRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r, ok := result.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("result type = %T, want *dto.GeneralOpenAIRequest", result)
	}
	if r.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o", r.Model)
	}
}

// ---------------------------------------------------------------------------
// Adaptor.Init — thinking_to_content branch
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// extractCachedTokensFromBody / extractMoonshotCachedTokensFromBody / applyUsagePostProcessing
// ---------------------------------------------------------------------------

func TestExtractCachedTokensFromBody(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantTokens int
		wantOK     bool
	}{
		{"empty body", "", 0, false},
		{"invalid json", "{not json", 0, false},
		{"prompt_tokens_details location", `{"usage":{"prompt_tokens_details":{"cached_tokens":42}}}`, 42, true},
		{"top-level usage.cached_tokens", `{"usage":{"cached_tokens":7}}`, 7, true},
		{"prompt_cache_hit_tokens fallback", `{"usage":{"prompt_cache_hit_tokens":3}}`, 3, true},
		{"no matching field", `{"usage":{"total_tokens":10}}`, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := extractCachedTokensFromBody([]byte(tt.body))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.wantTokens {
				t.Errorf("tokens = %d, want %d", got, tt.wantTokens)
			}
		})
	}
}

func TestExtractMoonshotCachedTokensFromBody(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantTokens int
		wantOK     bool
	}{
		{"empty body", "", 0, false},
		{"invalid json", "{not json", 0, false},
		{"choices usage cached_tokens", `{"choices":[{"usage":{"cached_tokens":111}}]}`, 111, true},
		{"zero cached_tokens skipped", `{"choices":[{"usage":{"cached_tokens":0}}]}`, 0, false},
		{"no choices", `{"choices":[]}`, 0, false},
		{"second choice has tokens", `{"choices":[{"usage":{"cached_tokens":0}},{"usage":{"cached_tokens":55}}]}`, 55, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := extractMoonshotCachedTokensFromBody([]byte(tt.body))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.wantTokens {
				t.Errorf("tokens = %d, want %d", got, tt.wantTokens)
			}
		})
	}
}

func TestApplyUsagePostProcessing(t *testing.T) {
	t.Run("nil info no-op", func(t *testing.T) {
		usage := &dto.Usage{}
		applyUsagePostProcessing(nil, usage, []byte(`{}`))
		if usage.PromptTokensDetails.CachedTokens != 0 {
			t.Error("expected no mutation with nil info")
		}
	})

	t.Run("nil usage no-op", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeDeepSeek}}
		// should not panic
		applyUsagePostProcessing(info, nil, []byte(`{}`))
	})

	t.Run("deepseek maps PromptCacheHitTokens into PromptTokensDetails", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeDeepSeek}}
		usage := &dto.Usage{PromptCacheHitTokens: 12}
		applyUsagePostProcessing(info, usage, nil)
		if usage.PromptTokensDetails.CachedTokens != 12 {
			t.Errorf("CachedTokens = %d, want 12", usage.PromptTokensDetails.CachedTokens)
		}
	})

	t.Run("deepseek does not override existing CachedTokens", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeDeepSeek}}
		usage := &dto.Usage{PromptCacheHitTokens: 12}
		usage.PromptTokensDetails.CachedTokens = 5
		applyUsagePostProcessing(info, usage, nil)
		if usage.PromptTokensDetails.CachedTokens != 5 {
			t.Errorf("CachedTokens = %d, want unchanged 5", usage.PromptTokensDetails.CachedTokens)
		}
	})

	t.Run("zhipu_v4 uses InputTokensDetails first", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeZhipu_v4}}
		usage := &dto.Usage{InputTokensDetails: &dto.InputTokenDetails{CachedTokens: 20}}
		applyUsagePostProcessing(info, usage, nil)
		if usage.PromptTokensDetails.CachedTokens != 20 {
			t.Errorf("CachedTokens = %d, want 20", usage.PromptTokensDetails.CachedTokens)
		}
	})

	t.Run("zhipu_v4 falls back to body extraction", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeZhipu_v4}}
		usage := &dto.Usage{}
		body := []byte(`{"usage":{"cached_tokens":33}}`)
		applyUsagePostProcessing(info, usage, body)
		if usage.PromptTokensDetails.CachedTokens != 33 {
			t.Errorf("CachedTokens = %d, want 33", usage.PromptTokensDetails.CachedTokens)
		}
	})

	t.Run("zhipu_v4 falls back to PromptCacheHitTokens last", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeZhipu_v4}}
		usage := &dto.Usage{PromptCacheHitTokens: 9}
		applyUsagePostProcessing(info, usage, []byte(`{}`))
		if usage.PromptTokensDetails.CachedTokens != 9 {
			t.Errorf("CachedTokens = %d, want 9", usage.PromptTokensDetails.CachedTokens)
		}
	})

	t.Run("moonshot uses moonshot-specific extraction over generic", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeMoonshot}}
		usage := &dto.Usage{}
		body := []byte(`{"choices":[{"usage":{"cached_tokens":77}}]}`)
		applyUsagePostProcessing(info, usage, body)
		if usage.PromptTokensDetails.CachedTokens != 77 {
			t.Errorf("CachedTokens = %d, want 77", usage.PromptTokensDetails.CachedTokens)
		}
	})

	t.Run("unrecognized channel type leaves usage untouched", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}
		usage := &dto.Usage{PromptCacheHitTokens: 100}
		applyUsagePostProcessing(info, usage, nil)
		if usage.PromptTokensDetails.CachedTokens != 0 {
			t.Errorf("CachedTokens = %d, want 0 (openai channel not handled)", usage.PromptTokensDetails.CachedTokens)
		}
	})
}

// ---------------------------------------------------------------------------
// preConsumeUsage — nil-guard branch only (no live quota consumption)
// ---------------------------------------------------------------------------

func TestPreConsumeUsage_NilGuard(t *testing.T) {
	c, _ := newTestGinContext(http.MethodPost, "/v1/realtime", nil)
	info := &relaycommon.RelayInfo{}

	t.Run("nil usage returns error", func(t *testing.T) {
		total := &dto.RealtimeUsage{}
		err := preConsumeUsage(c, info, nil, total)
		if err == nil {
			t.Fatal("expected error for nil usage")
		}
	})

	t.Run("nil totalUsage returns error", func(t *testing.T) {
		usage := &dto.RealtimeUsage{}
		err := preConsumeUsage(c, info, usage, nil)
		if err == nil {
			t.Fatal("expected error for nil totalUsage")
		}
	})
}

// ---------------------------------------------------------------------------
// OpenaiHandler (non-stream final response handler) — hermetic branches only
// ---------------------------------------------------------------------------

func newFakeUpstreamResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestOpenaiHandler_Success(t *testing.T) {
	c, w := newTestGinContext(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-4o"},
		RelayFormat: types.RelayFormatOpenAI,
	}
	body := `{"id":"chatcmpl-1","model":"gpt-4o","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`
	resp := newFakeUpstreamResponse(body)
	defer func() { _ = resp.Body.Close() }()
	usage, apiErr := OpenaiHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.PromptTokens != 5 || usage.CompletionTokens != 2 {
		t.Errorf("usage = %+v, want PromptTokens=5 CompletionTokens=2", usage)
	}
	if w.Code != http.StatusOK && w.Body.Len() == 0 {
		t.Errorf("expected body written to recorder")
	}
}

func TestOpenaiHandler_BadJSON(t *testing.T) {
	c, _ := newTestGinContext(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		RelayFormat: types.RelayFormatOpenAI,
	}
	resp := newFakeUpstreamResponse("{not json")
	defer func() { _ = resp.Body.Close() }()
	_, apiErr := OpenaiHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for invalid json response body")
	}
}

func TestOpenaiHandler_OpenAIErrorInBody(t *testing.T) {
	c, _ := newTestGinContext(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		RelayFormat: types.RelayFormatOpenAI,
	}
	body := `{"error":{"type":"invalid_request_error","message":"bad request"}}`
	resp := newFakeUpstreamResponse(body)
	defer func() { _ = resp.Body.Close() }()
	_, apiErr := OpenaiHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error when upstream body contains an OpenAI error object")
	}
}

// ---------------------------------------------------------------------------
// OpenaiHandlerWithUsage — hermetic success + error branches
// ---------------------------------------------------------------------------

func TestOpenaiHandlerWithUsage_Success(t *testing.T) {
	c, _ := newTestGinContext(http.MethodPost, "/v1/images/generations", nil)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
	}
	body := `{"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`
	resp := newFakeUpstreamResponse(body)
	defer func() { _ = resp.Body.Close() }()
	usage, apiErr := OpenaiHandlerWithUsage(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil || usage.PromptTokens != 3 {
		t.Errorf("usage = %+v, want PromptTokens=3", usage)
	}
}

func TestOpenaiHandlerWithUsage_BadJSON(t *testing.T) {
	c, _ := newTestGinContext(http.MethodPost, "/v1/images/generations", nil)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
	}
	resp := newFakeUpstreamResponse("{not json")
	defer func() { _ = resp.Body.Close() }()
	_, apiErr := OpenaiHandlerWithUsage(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for invalid json response body")
	}
}

func TestAdaptor_Init_ThinkingToContent(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    constant.ChannelTypeOpenAI,
			ChannelSetting: dto.ChannelSettings{ThinkingToContent: true},
		},
	}
	a.Init(info)
	if !info.IsFirstThinkingContent {
		t.Error("expected IsFirstThinkingContent = true after Init with ThinkingToContent enabled")
	}
	if info.HasSentThinkingContent {
		t.Error("expected HasSentThinkingContent = false after Init")
	}
}
