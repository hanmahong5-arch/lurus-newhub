package baidu_v2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/gin-gonic/gin"
)

func TestGetRequestURL(t *testing.T) {
	tests := []struct {
		name    string
		info    *relaycommon.RelayInfo
		wantURL string
		wantErr string
	}{
		{
			name: "chat completions",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://qianfan.baidubce.com"},
			},
			wantURL: "https://qianfan.baidubce.com/v2/chat/completions",
		},
		{
			name: "embeddings",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeEmbeddings,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://qianfan.baidubce.com"},
			},
			wantURL: "https://qianfan.baidubce.com/v2/embeddings",
		},
		{
			name: "images generations",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeImagesGenerations,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://qianfan.baidubce.com"},
			},
			wantURL: "https://qianfan.baidubce.com/v2/images/generations",
		},
		{
			name: "images edits",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeImagesEdits,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://qianfan.baidubce.com"},
			},
			wantURL: "https://qianfan.baidubce.com/v2/images/edits",
		},
		{
			name: "rerank",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeRerank,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://qianfan.baidubce.com"},
			},
			wantURL: "https://qianfan.baidubce.com/v2/rerank",
		},
		{
			name: "unsupported relay mode returns error",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://qianfan.baidubce.com"},
			},
			wantErr: "unsupported relay mode: 2",
		},
	}

	a := &Adaptor{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := a.GetRequestURL(tt.info)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				if got != "" {
					t.Errorf("expected empty url on error, got %q", got)
				}
				return
			}
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

	newCtx := func() *gin.Context {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		return c
	}

	a := &Adaptor{}

	t.Run("key with appid sets both headers", func(t *testing.T) {
		c := newCtx()
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-secret|app-123"}}
		header := http.Header{}

		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Authorization"); got != "Bearer sk-secret" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer sk-secret")
		}
		if got := header.Get("appid"); got != "app-123" {
			t.Errorf("appid = %q, want %q", got, "app-123")
		}
	})

	t.Run("key without pipe sets only Authorization", func(t *testing.T) {
		c := newCtx()
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-onlykey"}}
		header := http.Header{}

		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Authorization"); got != "Bearer sk-onlykey" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer sk-onlykey")
		}
		if got := header.Get("appid"); got != "" {
			t.Errorf("appid = %q, want empty", got)
		}
	})

	t.Run("key with empty appid segment does not set appid header", func(t *testing.T) {
		c := newCtx()
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-onlykey|"}}
		header := http.Header{}

		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Authorization"); got != "Bearer sk-onlykey" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer sk-onlykey")
		}
		if got := header.Get("appid"); got != "" {
			t.Errorf("appid = %q, want empty", got)
		}
	})

	t.Run("empty api key returns error", func(t *testing.T) {
		c := newCtx()
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: ""}}
		header := http.Header{}

		err := a.SetupRequestHeader(c, &header, info)
		if err == nil || err.Error() != "invalid API key: authorization token is required" {
			t.Fatalf("err = %v, want %q", err, "invalid API key: authorization token is required")
		}
	})
}

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("nil request returns error", func(t *testing.T) {
		got, err := a.ConvertOpenAIRequest(nil, nil, nil)
		if err == nil || err.Error() != "request is nil" {
			t.Fatalf("err = %v, want %q", err, "request is nil")
		}
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
	})

	t.Run("non-search model passes through unchanged", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "ernie-4.0-8k"}}
		req := &dto.GeneralOpenAIRequest{Model: "ernie-4.0-8k"}

		got, err := a.ConvertOpenAIRequest(nil, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotReq, ok := got.(*dto.GeneralOpenAIRequest)
		if !ok {
			t.Fatalf("expected *dto.GeneralOpenAIRequest, got %T", got)
		}
		if gotReq != req {
			t.Errorf("expected same pointer to be returned")
		}
		if info.UpstreamModelName != "ernie-4.0-8k" {
			t.Errorf("UpstreamModelName mutated unexpectedly: %q", info.UpstreamModelName)
		}
	})

	t.Run("search suffix strips suffix, rewrites model, injects web_search when absent", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "ernie-4.0-8k-search"}}
		req := &dto.GeneralOpenAIRequest{Model: "ernie-4.0-8k-search"}

		got, err := a.ConvertOpenAIRequest(nil, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if info.UpstreamModelName != "ernie-4.0-8k" {
			t.Errorf("UpstreamModelName = %q, want %q", info.UpstreamModelName, "ernie-4.0-8k")
		}
		if req.Model != "ernie-4.0-8k" {
			t.Errorf("request.Model = %q, want %q", req.Model, "ernie-4.0-8k")
		}

		gotMap, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("expected map[string]any, got %T", got)
		}
		webSearch, ok := gotMap["web_search"].(map[string]any)
		if !ok {
			t.Fatalf("expected web_search map in result, got %v", gotMap["web_search"])
		}
		wantWebSearch := map[string]any{
			"enable":          true,
			"enable_citation": true,
			"enable_trace":    true,
			"enable_status":   false,
		}
		for k, v := range wantWebSearch {
			if webSearch[k] != v {
				t.Errorf("web_search[%q] = %v, want %v", k, webSearch[k], v)
			}
		}
		if gotMap["model"] != "ernie-4.0-8k" {
			t.Errorf("model in map = %v, want %q", gotMap["model"], "ernie-4.0-8k")
		}
	})

	t.Run("search suffix with pre-existing web_search leaves request untouched and returns request itself", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "ernie-4.0-8k-search"}}
		req := &dto.GeneralOpenAIRequest{
			Model:     "ernie-4.0-8k-search",
			WebSearch: json.RawMessage(`{"already":"set"}`),
		}

		got, err := a.ConvertOpenAIRequest(nil, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.UpstreamModelName != "ernie-4.0-8k" {
			t.Errorf("UpstreamModelName = %q, want %q", info.UpstreamModelName, "ernie-4.0-8k")
		}
		gotReq, ok := got.(*dto.GeneralOpenAIRequest)
		if !ok {
			t.Fatalf("expected *dto.GeneralOpenAIRequest, got %T", got)
		}
		if gotReq != req {
			t.Errorf("expected same pointer to be returned")
		}
		if gotReq.Model != "ernie-4.0-8k" {
			t.Errorf("Model = %q, want %q", gotReq.Model, "ernie-4.0-8k")
		}
	})
}

func TestConvertClaudeRequest(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.ClaudeRequest{
		Model:     "claude-3-sonnet",
		MaxTokens: 256,
	}

	got, err := a.ConvertClaudeRequest(nil, info, req)
	if err != nil {
		t.Fatalf("unexpected error delegating to openai adaptor: %v", err)
	}
	gotReq, ok := got.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("expected *dto.GeneralOpenAIRequest, got %T", got)
	}
	if gotReq.Model != "claude-3-sonnet" {
		t.Errorf("Model = %q, want %q", gotReq.Model, "claude-3-sonnet")
	}
	if gotReq.MaxTokens != 256 {
		t.Errorf("MaxTokens = %d, want %d", gotReq.MaxTokens, 256)
	}
}

func TestConvertRerankRequest(t *testing.T) {
	a := &Adaptor{}
	got, err := a.ConvertRerankRequest(nil, 0, dto.RerankRequest{})
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err == nil || err.Error() != "not implemented" {
		t.Errorf("error = %v, want %q", err, "not implemented")
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

func TestInit(t *testing.T) {
	a := &Adaptor{}
	// Init is a no-op; calling it must not panic even with a nil info.
	a.Init(nil)
}

func TestGetModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}

	gotModels := a.GetModelList()
	if len(gotModels) != len(ModelList) {
		t.Fatalf("GetModelList() length = %d, want %d", len(gotModels), len(ModelList))
	}
	for i, m := range ModelList {
		if gotModels[i] != m {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, gotModels[i], m)
		}
	}
	if len(gotModels) == 0 {
		t.Fatal("expected a non-empty model list")
	}
	if gotModels[0] != "ernie-4.0-8k-latest" {
		t.Errorf("GetModelList()[0] = %q, want %q", gotModels[0], "ernie-4.0-8k-latest")
	}
	if gotModels[len(gotModels)-1] != "deepseek-r1-distill-qwen-14b" {
		t.Errorf("GetModelList() last = %q, want %q", gotModels[len(gotModels)-1], "deepseek-r1-distill-qwen-14b")
	}

	if got := a.GetChannelName(); got != "volcengine" {
		t.Errorf("GetChannelName() = %q, want %q", got, "volcengine")
	}
}

// NOTE: DoRequest and DoResponse both perform live HTTP / delegate to code
// paths that require a real gin.Context and upstream network access; they
// are not hermetically testable beyond what is already exercised indirectly
// via GetRequestURL/SetupRequestHeader/ConvertOpenAIRequest above.
