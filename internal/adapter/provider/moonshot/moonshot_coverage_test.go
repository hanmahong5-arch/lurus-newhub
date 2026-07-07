package moonshot

import (
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
	"github.com/gin-gonic/gin"
)

func TestGetRequestURL(t *testing.T) {
	tests := []struct {
		name    string
		info    *relaycommon.RelayInfo
		wantURL string
	}{
		{
			name: "special base + claude format uses ClaudeBaseURL messages endpoint",
			info: &relaycommon.RelayInfo{
				RelayFormat:    types.RelayFormatClaude,
				ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: "kimi-coding-plan"},
			},
			wantURL: "https://api.kimi.com/coding/v1/messages",
		},
		{
			name: "special base + openai format uses OpenAIBaseURL chat/completions endpoint",
			info: &relaycommon.RelayInfo{
				RelayFormat:    types.RelayFormatOpenAI,
				ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: "kimi-coding-plan"},
			},
			wantURL: "https://api.kimi.com/coding/v1/chat/completions",
		},
		{
			name: "special base + unmatched relay format falls through to generic switch (claude case unreachable, hits default chat completions)",
			info: &relaycommon.RelayInfo{
				RelayFormat:    types.RelayFormatGemini,
				ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: "kimi-coding-plan"},
			},
			wantURL: "kimi-coding-plan/v1/chat/completions",
		},
		{
			name: "non-special base + claude format uses anthropic messages endpoint",
			info: &relaycommon.RelayInfo{
				RelayFormat:    types.RelayFormatClaude,
				ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.moonshot.cn"},
			},
			wantURL: "https://api.moonshot.cn/anthropic/v1/messages",
		},
		{
			name: "non-special base + rerank mode",
			info: &relaycommon.RelayInfo{
				RelayFormat:    types.RelayFormatOpenAI,
				RelayMode:      constant.RelayModeRerank,
				ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.moonshot.cn"},
			},
			wantURL: "https://api.moonshot.cn/v1/rerank",
		},
		{
			name: "non-special base + embeddings mode",
			info: &relaycommon.RelayInfo{
				RelayFormat:    types.RelayFormatOpenAI,
				RelayMode:      constant.RelayModeEmbeddings,
				ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.moonshot.cn"},
			},
			wantURL: "https://api.moonshot.cn/v1/embeddings",
		},
		{
			name: "non-special base + chat completions mode",
			info: &relaycommon.RelayInfo{
				RelayFormat:    types.RelayFormatOpenAI,
				RelayMode:      constant.RelayModeChatCompletions,
				ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.moonshot.cn"},
			},
			wantURL: "https://api.moonshot.cn/v1/chat/completions",
		},
		{
			name: "non-special base + completions mode",
			info: &relaycommon.RelayInfo{
				RelayFormat:    types.RelayFormatOpenAI,
				RelayMode:      constant.RelayModeCompletions,
				ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.moonshot.cn"},
			},
			wantURL: "https://api.moonshot.cn/v1/completions",
		},
		{
			name: "non-special base + unknown mode falls into final default (chat completions)",
			info: &relaycommon.RelayInfo{
				RelayFormat:    types.RelayFormatOpenAI,
				ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.moonshot.cn"},
			},
			wantURL: "https://api.moonshot.cn/v1/chat/completions",
		},
	}

	a := &Adaptor{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := a.GetRequestURL(tt.info)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantURL {
				t.Errorf("GetRequestURL() = %q, want %q", got, tt.wantURL)
			}
		})
	}
}

func TestSetupRequestHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-moonshot-test"}}
	header := http.Header{}

	a := &Adaptor{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := header.Get("Authorization"); got != "Bearer sk-moonshot-test" {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer sk-moonshot-test")
	}
}

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}
	req := &dto.GeneralOpenAIRequest{Model: "moonshot-v1-8k"}
	got, err := a.ConvertOpenAIRequest(nil, nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotReq, ok := got.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("expected *dto.GeneralOpenAIRequest, got %T", got)
	}
	if gotReq != req {
		t.Errorf("expected the same pointer to be returned, got a different value")
	}
	if gotReq.Model != "moonshot-v1-8k" {
		t.Errorf("Model = %q, want %q", gotReq.Model, "moonshot-v1-8k")
	}
}

func TestConvertClaudeRequest(t *testing.T) {
	a := &Adaptor{}
	req := &dto.ClaudeRequest{Model: "moonshot-v1-32k"}
	got, err := a.ConvertClaudeRequest(nil, nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotReq, ok := got.(*dto.ClaudeRequest)
	if !ok {
		t.Fatalf("expected *dto.ClaudeRequest, got %T", got)
	}
	if gotReq != req {
		t.Errorf("expected the same pointer to be returned, got a different value")
	}
}

func TestConvertImageRequest(t *testing.T) {
	a := &Adaptor{}
	// RelayMode left at its zero value (RelayModeUnknown), which is not
	// RelayModeImagesEdits, so the delegated openai.Adaptor hits its
	// default branch and returns the request unchanged without touching
	// the gin.Context or performing any multipart parsing.
	info := &relaycommon.RelayInfo{}
	req := dto.ImageRequest{Model: "moonshot-v1-8k"}
	got, err := a.ConvertImageRequest(nil, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotReq, ok := got.(dto.ImageRequest)
	if !ok {
		t.Fatalf("expected dto.ImageRequest, got %T", got)
	}
	if gotReq.Model != "moonshot-v1-8k" {
		t.Errorf("Model = %q, want %q", gotReq.Model, "moonshot-v1-8k")
	}
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
		if err == nil || err.Error() != "not supported" {
			t.Errorf("error = %v, want %q", err, "not supported")
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
	req := dto.RerankRequest{Model: "moonshot-v1-8k"}
	got, err := a.ConvertRerankRequest(nil, 0, req)
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	gotReq, ok := got.(dto.RerankRequest)
	if !ok {
		t.Fatalf("expected dto.RerankRequest, got %T", got)
	}
	if gotReq.Model != "moonshot-v1-8k" {
		t.Errorf("Model = %q, want %q", gotReq.Model, "moonshot-v1-8k")
	}
}

func TestConvertEmbeddingRequest(t *testing.T) {
	a := &Adaptor{}
	req := dto.EmbeddingRequest{Model: "moonshot-v1-8k"}
	got, err := a.ConvertEmbeddingRequest(nil, nil, req)
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	gotReq, ok := got.(dto.EmbeddingRequest)
	if !ok {
		t.Fatalf("expected dto.EmbeddingRequest, got %T", got)
	}
	if gotReq.Model != "moonshot-v1-8k" {
		t.Errorf("Model = %q, want %q", gotReq.Model, "moonshot-v1-8k")
	}
}

func TestInit(t *testing.T) {
	a := &Adaptor{}
	// Init is a no-op; calling it must not panic and must accept a nil info
	// (there is nothing to assert on state since the struct has no fields).
	a.Init(nil)
}

func TestGetModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}

	wantModels := []string{"moonshot-v1-8k", "moonshot-v1-32k", "moonshot-v1-128k"}
	gotModels := a.GetModelList()
	if len(gotModels) != len(wantModels) {
		t.Fatalf("GetModelList() length = %d, want %d", len(gotModels), len(wantModels))
	}
	for i, m := range wantModels {
		if gotModels[i] != m {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, gotModels[i], m)
		}
	}

	if got := a.GetChannelName(); got != "moonshot" {
		t.Errorf("GetChannelName() = %q, want %q", got, "moonshot")
	}
}
