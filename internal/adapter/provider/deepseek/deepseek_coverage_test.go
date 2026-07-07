package deepseek

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
			name: "claude format uses anthropic messages endpoint",
			info: &relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatClaude,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.deepseek.com"},
			},
			wantURL: "https://api.deepseek.com/anthropic/v1/messages",
		},
		{
			name: "default format + completions mode, base url without /beta suffix",
			info: &relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatOpenAI,
				RelayMode:   constant.RelayModeCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.deepseek.com"},
			},
			wantURL: "https://api.deepseek.com/beta/completions",
		},
		{
			name: "default format + completions mode, base url already has /beta suffix",
			info: &relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatOpenAI,
				RelayMode:   constant.RelayModeCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.deepseek.com/beta"},
			},
			wantURL: "https://api.deepseek.com/beta/completions",
		},
		{
			name: "default format + chat completions mode",
			info: &relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatOpenAI,
				RelayMode:   constant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.deepseek.com"},
			},
			wantURL: "https://api.deepseek.com/v1/chat/completions",
		},
		{
			name: "empty relay format falls into default branch (chat completions)",
			info: &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.deepseek.com"},
			},
			wantURL: "https://api.deepseek.com/v1/chat/completions",
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

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-test-key"}}
	header := http.Header{}

	a := &Adaptor{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := header.Get("Authorization"); got != "Bearer sk-test-key" {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer sk-test-key")
	}
}

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("nil request returns error", func(t *testing.T) {
		got, err := a.ConvertOpenAIRequest(nil, nil, nil)
		if err == nil {
			t.Fatal("expected error for nil request, got nil")
		}
		if err.Error() != "request is nil" {
			t.Errorf("error = %q, want %q", err.Error(), "request is nil")
		}
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
	})

	t.Run("non-nil request is passed through unchanged", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "deepseek-chat"}
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
		if gotReq.Model != "deepseek-chat" {
			t.Errorf("Model = %q, want %q", gotReq.Model, "deepseek-chat")
		}
	})
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
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("error = %v, want %q", err, "not implemented")
		}
	})

	t.Run("ConvertImageRequest", func(t *testing.T) {
		got, err := a.ConvertImageRequest(nil, nil, dto.ImageRequest{})
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("error = %v, want %q", err, "not implemented")
		}
	})

	t.Run("ConvertEmbeddingRequest", func(t *testing.T) {
		got, err := a.ConvertEmbeddingRequest(nil, nil, dto.EmbeddingRequest{})
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "not implemented" {
			t.Errorf("error = %v, want %q", err, "not implemented")
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

func TestConvertClaudeRequest(t *testing.T) {
	a := &Adaptor{}
	req := &dto.ClaudeRequest{Model: "deepseek-chat"}
	got, err := a.ConvertClaudeRequest(nil, nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotReq, ok := got.(*dto.ClaudeRequest)
	if !ok {
		t.Fatalf("expected *dto.ClaudeRequest, got %T", got)
	}
	if gotReq != req {
		t.Errorf("expected pass-through of the same pointer, got a different value")
	}
	if gotReq.Model != "deepseek-chat" {
		t.Errorf("Model = %q, want %q", gotReq.Model, "deepseek-chat")
	}
}

func TestConvertRerankRequest(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertRerankRequest(nil, 0, dto.RerankRequest{})
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
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

	wantModels := []string{"deepseek-chat", "deepseek-reasoner"}
	gotModels := a.GetModelList()
	if len(gotModels) != len(wantModels) {
		t.Fatalf("GetModelList() length = %d, want %d", len(gotModels), len(wantModels))
	}
	for i, m := range wantModels {
		if gotModels[i] != m {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, gotModels[i], m)
		}
	}

	if got := a.GetChannelName(); got != "deepseek" {
		t.Errorf("GetChannelName() = %q, want %q", got, "deepseek")
	}
}
