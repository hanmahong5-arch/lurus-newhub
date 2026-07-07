package ali

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	pkgconstant "github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
	// Multipart/JSON request bodies built in this file are tiny in-memory
	// fixtures; disable the size cap so UnmarshalBodyReusable doesn't reject
	// them (production sets this from env/config, which is 0 by default in
	// a bare test binary).
	pkgconstant.MaxRequestBodyMB = -1
}

// ---------------------------------------------------------------------
// constants / metadata
// ---------------------------------------------------------------------

func TestGetModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}

	want := []string{
		"qwen-turbo",
		"qwen-plus",
		"qwen-max",
		"qwen-max-longcontext",
		"qwq-32b",
		"qwen3-235b-a22b",
		"text-embedding-v1",
		"gte-rerank-v2",
	}
	got := a.GetModelList()
	if len(got) != len(want) {
		t.Fatalf("GetModelList() length = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("GetModelList()[%d] = %q, want %q", i, got[i], w)
		}
	}

	if name := a.GetChannelName(); name != "ali" {
		t.Errorf("GetChannelName() = %q, want %q", name, "ali")
	}
}

func TestInit(t *testing.T) {
	a := &Adaptor{}
	// Init is a no-op; must not panic on a nil info.
	a.Init(nil)
}

// ---------------------------------------------------------------------
// isSyncImageModel / isOldWanModel / isWanModel (private helpers)
// ---------------------------------------------------------------------

func TestIsSyncImageModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"z-image-turbo", true},
		{"qwen-image-plus", true},
		{"wan2.6-t2i", true},
		{"wanx2.1-t2i-turbo", false},
		{"qwen-turbo", false},
	}
	for _, tt := range tests {
		if got := isSyncImageModel(tt.model); got != tt.want {
			t.Errorf("isSyncImageModel(%q) = %v, want %v", tt.model, got, tt.want)
		}
	}
}

func TestIsOldWanModelAndIsWanModel(t *testing.T) {
	tests := []struct {
		model       string
		wantOldWan  bool
		wantWanBase bool
	}{
		{"wanx2.1-t2i-turbo", true, true},
		{"wan2.6-t2i", false, true},
		{"qwen-turbo", false, false},
	}
	for _, tt := range tests {
		if got := isOldWanModel(tt.model); got != tt.wantOldWan {
			t.Errorf("isOldWanModel(%q) = %v, want %v", tt.model, got, tt.wantOldWan)
		}
		if got := isWanModel(tt.model); got != tt.wantWanBase {
			t.Errorf("isWanModel(%q) = %v, want %v", tt.model, got, tt.wantWanBase)
		}
	}
}

// ---------------------------------------------------------------------
// GetRequestURL
// ---------------------------------------------------------------------

func TestGetRequestURL(t *testing.T) {
	tests := []struct {
		name string
		info *relaycommon.RelayInfo
		want string
	}{
		{
			name: "claude format",
			info: &relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatClaude,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://dashscope.aliyuncs.com"},
			},
			want: "https://dashscope.aliyuncs.com/api/v2/apps/claude-code-proxy/v1/messages",
		},
		{
			name: "embeddings",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeEmbeddings,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://dashscope.aliyuncs.com"},
			},
			want: "https://dashscope.aliyuncs.com/compatible-mode/v1/embeddings",
		},
		{
			name: "rerank",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeRerank,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://dashscope.aliyuncs.com"},
			},
			want: "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank",
		},
		{
			name: "images generations - sync model",
			info: &relaycommon.RelayInfo{
				RelayMode:       constant.RelayModeImagesGenerations,
				OriginModelName: "z-image-turbo",
				ChannelMeta:     &relaycommon.ChannelMeta{ChannelBaseUrl: "https://dashscope.aliyuncs.com"},
			},
			want: "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation",
		},
		{
			name: "images generations - async model",
			info: &relaycommon.RelayInfo{
				RelayMode:       constant.RelayModeImagesGenerations,
				OriginModelName: "wanx2.1-t2i-turbo",
				ChannelMeta:     &relaycommon.ChannelMeta{ChannelBaseUrl: "https://dashscope.aliyuncs.com"},
			},
			want: "https://dashscope.aliyuncs.com/api/v1/services/aigc/text2image/image-synthesis",
		},
		{
			name: "images edits - old wan model",
			info: &relaycommon.RelayInfo{
				RelayMode:       constant.RelayModeImagesEdits,
				OriginModelName: "wanx2.1-t2i-turbo",
				ChannelMeta:     &relaycommon.ChannelMeta{ChannelBaseUrl: "https://dashscope.aliyuncs.com"},
			},
			want: "https://dashscope.aliyuncs.com/api/v1/services/aigc/image2image/image-synthesis",
		},
		{
			name: "images edits - new wan model",
			info: &relaycommon.RelayInfo{
				RelayMode:       constant.RelayModeImagesEdits,
				OriginModelName: "wan2.6-edit",
				ChannelMeta:     &relaycommon.ChannelMeta{ChannelBaseUrl: "https://dashscope.aliyuncs.com"},
			},
			want: "https://dashscope.aliyuncs.com/api/v1/services/aigc/image-generation/generation",
		},
		{
			name: "images edits - non wan model",
			info: &relaycommon.RelayInfo{
				RelayMode:       constant.RelayModeImagesEdits,
				OriginModelName: "qwen-image-edit",
				ChannelMeta:     &relaycommon.ChannelMeta{ChannelBaseUrl: "https://dashscope.aliyuncs.com"},
			},
			want: "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation",
		},
		{
			name: "completions",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://dashscope.aliyuncs.com"},
			},
			want: "https://dashscope.aliyuncs.com/compatible-mode/v1/completions",
		},
		{
			name: "default falls back to chat completions",
			info: &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://dashscope.aliyuncs.com"},
			},
			want: "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions",
		},
	}

	a := &Adaptor{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := a.GetRequestURL(tt.info)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("GetRequestURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------

func newTestGinContext(t *testing.T, method, path string, body io.Reader) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, body)
	return c, w
}

func TestSetupRequestHeader(t *testing.T) {
	t.Run("basic auth + stream header", func(t *testing.T) {
		c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-test"}, IsStream: true}
		header := http.Header{}

		a := &Adaptor{}
		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer sk-test")
		}
		if got := header.Get("X-DashScope-SSE"); got != "enable" {
			t.Errorf("X-DashScope-SSE = %q, want %q", got, "enable")
		}
		if got := header.Get("X-DashScope-Plugin"); got != "" {
			t.Errorf("X-DashScope-Plugin = %q, want empty", got)
		}
	})

	t.Run("no stream -> no SSE header", func(t *testing.T) {
		c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-test"}, IsStream: false}
		header := http.Header{}

		a := &Adaptor{}
		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("X-DashScope-SSE"); got != "" {
			t.Errorf("X-DashScope-SSE = %q, want empty", got)
		}
	})

	t.Run("plugin context key sets plugin header", func(t *testing.T) {
		c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
		c.Set("plugin", "my-plugin")
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-test"}}
		header := http.Header{}

		a := &Adaptor{}
		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("X-DashScope-Plugin"); got != "my-plugin" {
			t.Errorf("X-DashScope-Plugin = %q, want %q", got, "my-plugin")
		}
	})

	t.Run("images generations sync model -> no async header", func(t *testing.T) {
		c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{
			ChannelMeta:     &relaycommon.ChannelMeta{ApiKey: "sk-test"},
			RelayMode:       constant.RelayModeImagesGenerations,
			OriginModelName: "z-image-turbo",
		}
		header := http.Header{}

		a := &Adaptor{}
		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("X-DashScope-Async"); got != "" {
			t.Errorf("X-DashScope-Async = %q, want empty for sync model", got)
		}
	})

	t.Run("images generations async model -> async header enabled", func(t *testing.T) {
		c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{
			ChannelMeta:     &relaycommon.ChannelMeta{ApiKey: "sk-test"},
			RelayMode:       constant.RelayModeImagesGenerations,
			OriginModelName: "wanx2.1-t2i-turbo",
		}
		header := http.Header{}

		a := &Adaptor{}
		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("X-DashScope-Async"); got != "enable" {
			t.Errorf("X-DashScope-Async = %q, want %q", got, "enable")
		}
	})

	t.Run("images edits wan model -> async header + content-type json", func(t *testing.T) {
		c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{
			ChannelMeta:     &relaycommon.ChannelMeta{ApiKey: "sk-test"},
			RelayMode:       constant.RelayModeImagesEdits,
			OriginModelName: "wan2.6-edit",
		}
		header := http.Header{}

		a := &Adaptor{}
		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("X-DashScope-Async"); got != "enable" {
			t.Errorf("X-DashScope-Async = %q, want %q", got, "enable")
		}
		if got := header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want %q", got, "application/json")
		}
	})

	t.Run("images edits non-wan model -> no async header but content-type json", func(t *testing.T) {
		c, _ := newTestGinContext(t, http.MethodPost, "/", nil)
		info := &relaycommon.RelayInfo{
			ChannelMeta:     &relaycommon.ChannelMeta{ApiKey: "sk-test"},
			RelayMode:       constant.RelayModeImagesEdits,
			OriginModelName: "qwen-image-edit",
		}
		header := http.Header{}

		a := &Adaptor{}
		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("X-DashScope-Async"); got != "" {
			t.Errorf("X-DashScope-Async = %q, want empty", got)
		}
		if got := header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want %q", got, "application/json")
		}
	})
}

// ---------------------------------------------------------------------
// ConvertOpenAIRequest / requestOpenAI2Ali
// ---------------------------------------------------------------------

func TestConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}

	t.Run("nil request errors", func(t *testing.T) {
		got, err := a.ConvertOpenAIRequest(nil, nil, nil)
		if got != nil {
			t.Errorf("expected nil result, got %v", got)
		}
		if err == nil || err.Error() != "request is nil" {
			t.Errorf("error = %v, want %q", err, "request is nil")
		}
	})

	t.Run("TopP >= 1 clamps to 0.999", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "qwen-turbo", TopP: 1.0}
		got, err := a.ConvertOpenAIRequest(nil, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out, ok := got.(*dto.GeneralOpenAIRequest)
		if !ok {
			t.Fatalf("expected *dto.GeneralOpenAIRequest, got %T", got)
		}
		if out.TopP != 0.999 {
			t.Errorf("TopP = %v, want 0.999", out.TopP)
		}
	})

	t.Run("TopP <= 0 clamps to 0.001", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "qwen-turbo", TopP: 0}
		got, err := a.ConvertOpenAIRequest(nil, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out := got.(*dto.GeneralOpenAIRequest)
		if out.TopP != 0.001 {
			t.Errorf("TopP = %v, want 0.001", out.TopP)
		}
	})

	t.Run("TopP in (0,1) is unchanged", func(t *testing.T) {
		req := &dto.GeneralOpenAIRequest{Model: "qwen-turbo", TopP: 0.5}
		got, err := a.ConvertOpenAIRequest(nil, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out := got.(*dto.GeneralOpenAIRequest)
		if out.TopP != 0.5 {
			t.Errorf("TopP = %v, want 0.5", out.TopP)
		}
	})
}

// ---------------------------------------------------------------------
// ConvertClaudeRequest / ConvertRerankRequest / ConvertEmbeddingRequest
// ---------------------------------------------------------------------

func TestConvertClaudeRequest(t *testing.T) {
	a := &Adaptor{}
	req := &dto.ClaudeRequest{Model: "claude-3"}
	got, err := a.ConvertClaudeRequest(nil, nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != req {
		t.Errorf("expected the same pointer returned, got %v", got)
	}
}

func TestAdaptorConvertRerankRequest(t *testing.T) {
	a := &Adaptor{}
	returnDocs := false
	req := dto.RerankRequest{
		Model:           "gte-rerank-v2",
		Query:           "hello",
		Documents:       []any{"a", "b"},
		TopN:            5,
		ReturnDocuments: &returnDocs,
	}
	got, err := a.ConvertRerankRequest(nil, 0, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	aliReq, ok := got.(*AliRerankRequest)
	if !ok {
		t.Fatalf("expected *AliRerankRequest, got %T", got)
	}
	if aliReq.Model != "gte-rerank-v2" || aliReq.Input.Query != "hello" {
		t.Errorf("unexpected conversion: %+v", aliReq)
	}
	if aliReq.Parameters.TopN == nil || *aliReq.Parameters.TopN != 5 {
		t.Errorf("TopN = %v, want 5", aliReq.Parameters.TopN)
	}
	if aliReq.Parameters.ReturnDocuments == nil || *aliReq.Parameters.ReturnDocuments != false {
		t.Errorf("ReturnDocuments = %v, want false", aliReq.Parameters.ReturnDocuments)
	}
}

func TestConvertEmbeddingRequest(t *testing.T) {
	a := &Adaptor{}
	req := dto.EmbeddingRequest{Model: "text-embedding-v1"}
	got, err := a.ConvertEmbeddingRequest(nil, nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, ok := got.(dto.EmbeddingRequest)
	if !ok {
		t.Fatalf("expected dto.EmbeddingRequest, got %T", got)
	}
	if out.Model != "text-embedding-v1" {
		t.Errorf("Model = %q, want %q", out.Model, "text-embedding-v1")
	}
}

// ---------------------------------------------------------------------
// not-implemented stubs
// ---------------------------------------------------------------------

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

// ---------------------------------------------------------------------
// ConvertImageRequest
// ---------------------------------------------------------------------

func TestConvertImageRequest_UnsupportedMode(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeChatCompletions}
	got, err := a.ConvertImageRequest(nil, info, dto.ImageRequest{})
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
	if err == nil {
		t.Fatal("expected error for unsupported relay mode")
	}
}

func TestConvertImageRequest_Generations(t *testing.T) {
	t.Run("sync model builds sync input", func(t *testing.T) {
		a := &Adaptor{}
		info := &relaycommon.RelayInfo{
			RelayMode:       constant.RelayModeImagesGenerations,
			OriginModelName: "z-image-turbo",
		}
		req := dto.ImageRequest{Model: "z-image-turbo", Prompt: "a cat"}
		got, err := a.ConvertImageRequest(nil, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !a.IsSyncImageModel {
			t.Error("expected IsSyncImageModel to be set true")
		}
		aliReq, ok := got.(*AliImageRequest)
		if !ok {
			t.Fatalf("expected *AliImageRequest, got %T", got)
		}
		input, ok := aliReq.Input.(AliImageInput)
		if !ok {
			t.Fatalf("expected AliImageInput for sync model, got %T", aliReq.Input)
		}
		if len(input.Messages) != 1 || input.Messages[0].Role != "user" {
			t.Errorf("unexpected sync input messages: %+v", input.Messages)
		}
	})

	t.Run("async model builds prompt-only input", func(t *testing.T) {
		a := &Adaptor{}
		info := &relaycommon.RelayInfo{
			RelayMode:       constant.RelayModeImagesGenerations,
			OriginModelName: "wanx2.1-t2i-turbo",
		}
		req := dto.ImageRequest{Model: "wanx2.1-t2i-turbo", Prompt: "a dog"}
		got, err := a.ConvertImageRequest(nil, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if a.IsSyncImageModel {
			t.Error("expected IsSyncImageModel to remain false")
		}
		aliReq := got.(*AliImageRequest)
		input, ok := aliReq.Input.(AliImageInput)
		if !ok {
			t.Fatalf("expected AliImageInput, got %T", aliReq.Input)
		}
		if input.Prompt != "a dog" {
			t.Errorf("Prompt = %q, want %q", input.Prompt, "a dog")
		}
	})

	t.Run("invalid extra parameters field errors", func(t *testing.T) {
		a := &Adaptor{}
		info := &relaycommon.RelayInfo{
			RelayMode:       constant.RelayModeImagesGenerations,
			OriginModelName: "z-image-turbo",
		}
		req := dto.ImageRequest{
			Model:  "z-image-turbo",
			Prompt: "x",
			Extra: map[string]json.RawMessage{
				"parameters": json.RawMessage(`"not-an-object"`),
			},
		}
		_, err := a.ConvertImageRequest(nil, info, req)
		if err == nil {
			t.Fatal("expected error for invalid parameters field")
		}
	})
}

func TestConvertImageRequest_EditsOldWan(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:       constant.RelayModeImagesEdits,
		OriginModelName: "wanx2.1-t2i-turbo",
		PriceData:       types.PriceData{},
	}
	c, w := multipartImageEditContext(t, "prompt-value")
	_ = w
	req := dto.ImageRequest{Model: "wanx2.1-t2i-turbo", Prompt: "fallback prompt", N: 2}
	got, err := a.ConvertImageRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	aliReq, ok := got.(*AliImageRequest)
	if !ok {
		t.Fatalf("expected *AliImageRequest, got %T", got)
	}
	wanInput, ok := aliReq.Input.(WanImageInput)
	if !ok {
		t.Fatalf("expected WanImageInput, got %T", aliReq.Input)
	}
	if len(wanInput.Images) != 1 {
		t.Errorf("Images len = %d, want 1", len(wanInput.Images))
	}
	if aliReq.Parameters.N != 2 {
		t.Errorf("Parameters.N = %d, want 2", aliReq.Parameters.N)
	}
	if info.PriceData.OtherRatios["n"] != 2 {
		t.Errorf("PriceData n ratio = %v, want 2", info.PriceData.OtherRatios["n"])
	}
}

func TestConvertImageRequest_EditsWanModelNonMultipart(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:       constant.RelayModeImagesEdits,
		OriginModelName: "wan2.6-edit",
	}
	c, _ := newTestGinContext(t, http.MethodPost, "/", bytes.NewBufferString("{}"))
	c.Request.Header.Set("Content-Type", "application/json")
	req := dto.ImageRequest{Model: "wan2.6-edit", Prompt: "edit this"}
	got, err := a.ConvertImageRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	aliReq := got.(*AliImageRequest)
	input, ok := aliReq.Input.(AliImageInput)
	if !ok {
		t.Fatalf("expected AliImageInput, got %T", aliReq.Input)
	}
	if input.Prompt != "edit this" {
		t.Errorf("Prompt = %q, want %q", input.Prompt, "edit this")
	}
	// isWanModel(true) but isOldWanModel(false) sets IsSyncImageModel=false via the
	// "else" (isWanModel) branch.
	if a.IsSyncImageModel {
		t.Error("expected IsSyncImageModel false for new wan model edits")
	}
}

func TestConvertImageRequest_EditsNonWanMultipart(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:       constant.RelayModeImagesEdits,
		OriginModelName: "qwen-image-edit",
	}
	c, _ := multipartImageEditContext(t, "unused")
	req := dto.ImageRequest{Model: "qwen-image-edit", Prompt: "edit prompt"}
	got, err := a.ConvertImageRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	aliReq, ok := got.(*AliImageRequest)
	if !ok {
		t.Fatalf("expected *AliImageRequest, got %T", got)
	}
	input, ok := aliReq.Input.(AliImageInput)
	if !ok {
		t.Fatalf("expected AliImageInput, got %T", aliReq.Input)
	}
	if len(input.Messages) != 1 {
		t.Fatalf("Messages len = %d, want 1", len(input.Messages))
	}
	content, ok := input.Messages[0].Content.([]AliMediaContent)
	if !ok {
		t.Fatalf("expected []AliMediaContent, got %T", input.Messages[0].Content)
	}
	if len(content) != 2 {
		t.Fatalf("Content len = %d, want 2 (image + text)", len(content))
	}
	if content[1].Text != "edit prompt" {
		t.Errorf("trailing text content = %q, want %q", content[1].Text, "edit prompt")
	}
}

func TestConvertImageRequest_EditsNonWanNonMultipart(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:       constant.RelayModeImagesEdits,
		OriginModelName: "qwen-image-edit",
	}
	c, _ := newTestGinContext(t, http.MethodPost, "/", bytes.NewBufferString("{}"))
	c.Request.Header.Set("Content-Type", "application/json")
	req := dto.ImageRequest{Model: "qwen-image-edit", Prompt: "json edit"}
	got, err := a.ConvertImageRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	aliReq := got.(*AliImageRequest)
	input, ok := aliReq.Input.(AliImageInput)
	if !ok {
		t.Fatalf("expected AliImageInput, got %T", aliReq.Input)
	}
	// "qwen-image-edit" matches the sync-image-model list, so
	// oaiImage2AliImageRequest is invoked with isSync=true and builds a
	// Messages-based input (text content) rather than a bare Prompt.
	if !a.IsSyncImageModel {
		t.Error("expected IsSyncImageModel true for qwen-image sync model")
	}
	if len(input.Messages) != 1 {
		t.Fatalf("Messages len = %d, want 1", len(input.Messages))
	}
	content, ok := input.Messages[0].Content.([]AliMediaContent)
	if !ok {
		t.Fatalf("expected []AliMediaContent, got %T", input.Messages[0].Content)
	}
	if len(content) != 1 || content[0].Text != "json edit" {
		t.Errorf("Content = %+v, want text %q", content, "json edit")
	}
}

func TestConvertImageRequest_MultipartFormParseFailure(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:       constant.RelayModeImagesEdits,
		OriginModelName: "qwen-image-edit",
	}
	c, _ := newTestGinContext(t, http.MethodPost, "/", bytes.NewBufferString("garbage"))
	c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=doesnotmatch")
	_, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Model: "qwen-image-edit", Prompt: "x"})
	if err == nil {
		t.Fatal("expected error for malformed multipart body")
	}
}

// multipartImageEditContext builds a gin context whose request carries a
// multipart/form-data body with a single "image" file field and a "prompt"
// text field, hermetically (no network / disk I/O).
func multipartImageEditContext(t *testing.T, prompt string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	fw, err := w.CreateFormFile("image", "input.png")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := w.WriteField("prompt", prompt); err != nil {
		t.Fatalf("write field: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	c, rec := newTestGinContext(t, http.MethodPost, "/", buf)
	c.Request.Header.Set("Content-Type", w.FormDataContentType())
	return c, rec
}

// ---------------------------------------------------------------------
// getImageBase64sFromForm
// ---------------------------------------------------------------------

func TestGetImageBase64sFromForm(t *testing.T) {
	t.Run("standard image field", func(t *testing.T) {
		c, _ := multipartImageEditContext(t, "p")
		got, err := getImageBase64sFromForm(c, "image")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1", len(got))
		}
		if got[0][:5] != "data:" {
			t.Errorf("expected data URL prefix, got %q", got[0][:5])
		}
	})

	t.Run("image[] field", func(t *testing.T) {
		buf := &bytes.Buffer{}
		w := multipart.NewWriter(buf)
		fw, _ := w.CreateFormFile("image[]", "a.png")
		_, _ = fw.Write([]byte("hello"))
		_ = w.Close()
		c, _ := newTestGinContext(t, http.MethodPost, "/", buf)
		c.Request.Header.Set("Content-Type", w.FormDataContentType())

		got, err := getImageBase64sFromForm(c, "image")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1", len(got))
		}
	})

	t.Run("image[0] array-style field", func(t *testing.T) {
		buf := &bytes.Buffer{}
		w := multipart.NewWriter(buf)
		fw, _ := w.CreateFormFile("image[0]", "a.png")
		_, _ = fw.Write([]byte("hello"))
		_ = w.Close()
		c, _ := newTestGinContext(t, http.MethodPost, "/", buf)
		c.Request.Header.Set("Content-Type", w.FormDataContentType())

		got, err := getImageBase64sFromForm(c, "image")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1", len(got))
		}
	})

	t.Run("no image field errors", func(t *testing.T) {
		buf := &bytes.Buffer{}
		w := multipart.NewWriter(buf)
		_ = w.WriteField("other", "value")
		_ = w.Close()
		c, _ := newTestGinContext(t, http.MethodPost, "/", buf)
		c.Request.Header.Set("Content-Type", w.FormDataContentType())

		_, err := getImageBase64sFromForm(c, "image")
		if err == nil || err.Error() != "image is required" {
			t.Errorf("error = %v, want %q", err, "image is required")
		}
	})
}

// ---------------------------------------------------------------------
// oaiFormEdit2WanxImageEdit
// ---------------------------------------------------------------------

func TestOaiFormEdit2WanxImageEdit(t *testing.T) {
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	fw, err := w.CreateFormFile("image", "input.png")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write([]byte("fakeimagebytes")); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := w.WriteField("negative_prompt", "no blur"); err != nil {
		t.Fatalf("write field: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	c, _ := newTestGinContext(t, http.MethodPost, "/", buf)
	c.Request.Header.Set("Content-Type", w.FormDataContentType())

	info := &relaycommon.RelayInfo{}
	req := dto.ImageRequest{Model: "wanx2.1-t2i-turbo", Prompt: "a river", N: 3}

	got, err := oaiFormEdit2WanxImageEdit(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Model != "wanx2.1-t2i-turbo" {
		t.Errorf("Model = %q, want %q", got.Model, "wanx2.1-t2i-turbo")
	}
	wanInput, ok := got.Input.(WanImageInput)
	if !ok {
		t.Fatalf("expected WanImageInput, got %T", got.Input)
	}
	if len(wanInput.Images) != 1 {
		t.Errorf("Images len = %d, want 1", len(wanInput.Images))
	}
	if got.Parameters.N != 3 {
		t.Errorf("Parameters.N = %d, want 3", got.Parameters.N)
	}
	if info.PriceData.OtherRatios["n"] != 3 {
		t.Errorf("PriceData n ratio = %v, want 3", info.PriceData.OtherRatios["n"])
	}
}

func TestOaiFormEdit2WanxImageEdit_MissingImageErrors(t *testing.T) {
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	_ = w.WriteField("negative_prompt", "no blur")
	_ = w.Close()
	c, _ := newTestGinContext(t, http.MethodPost, "/", buf)
	c.Request.Header.Set("Content-Type", w.FormDataContentType())

	info := &relaycommon.RelayInfo{}
	req := dto.ImageRequest{Model: "wanx2.1-t2i-turbo", Prompt: "a river"}

	_, err := oaiFormEdit2WanxImageEdit(c, info, req)
	if err == nil {
		t.Fatal("expected error when image field missing")
	}
}

// ---------------------------------------------------------------------
// oaiImage2AliImageRequest (direct, via image.go)
// ---------------------------------------------------------------------

func TestOaiImage2AliImageRequest_ZImagePromptExtendPricing(t *testing.T) {
	info := &relaycommon.RelayInfo{}
	promptExtend := true
	req := dto.ImageRequest{
		Model:  "z-image-turbo",
		Prompt: "a scene",
		Extra: map[string]json.RawMessage{
			"parameters": json.RawMessage(`{"prompt_extend":true}`),
		},
	}
	_ = promptExtend
	got, err := oaiImage2AliImageRequest(info, req, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Parameters.PromptExtendValue() {
		t.Error("expected PromptExtend true from parsed extra parameters")
	}
	if info.PriceData.OtherRatios["prompt_extend"] != 2 {
		t.Errorf("prompt_extend ratio = %v, want 2", info.PriceData.OtherRatios["prompt_extend"])
	}
}

func TestOaiImage2AliImageRequest_InvalidInputFieldErrors(t *testing.T) {
	info := &relaycommon.RelayInfo{}
	req := dto.ImageRequest{
		Model:  "qwen-turbo",
		Prompt: "x",
		Extra: map[string]json.RawMessage{
			"input": json.RawMessage(`123`), // valid JSON number, invalid for struct/any decode may still succeed for `any`; use malformed JSON instead
		},
	}
	// Use genuinely malformed JSON to force an unmarshal error.
	req.Extra["input"] = json.RawMessage(`{not-json`)
	_, err := oaiImage2AliImageRequest(info, req, true)
	if err == nil {
		t.Fatal("expected error for invalid input field")
	}
}

func TestOaiImage2AliImageRequest_FallbackFromStandardFields(t *testing.T) {
	info := &relaycommon.RelayInfo{}
	watermark := true
	req := dto.ImageRequest{
		Model:     "qwen-turbo",
		Prompt:    "a fox",
		Size:      "1024x1024",
		N:         2,
		Watermark: &watermark,
		// A non-nil Extra map (without a "parameters" key) is required to
		// reach the "compat" fallback branch that derives Parameters from
		// the standard OpenAI fields; a nil Extra (as produced by
		// constructing the struct directly rather than via UnmarshalJSON)
		// skips that whole block.
		Extra: map[string]json.RawMessage{},
	}
	got, err := oaiImage2AliImageRequest(info, req, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Parameters.Size != "1024*1024" {
		t.Errorf("Size = %q, want %q", got.Parameters.Size, "1024*1024")
	}
	if got.Parameters.N != 2 {
		t.Errorf("N = %d, want 2", got.Parameters.N)
	}
	if info.PriceData.OtherRatios["n"] != 2 {
		t.Errorf("PriceData n ratio = %v, want 2", info.PriceData.OtherRatios["n"])
	}
	input, ok := got.Input.(AliImageInput)
	if !ok {
		t.Fatalf("expected AliImageInput for async model, got %T", got.Input)
	}
	if input.Prompt != "a fox" {
		t.Errorf("Prompt = %q, want %q", input.Prompt, "a fox")
	}
}

// ---------------------------------------------------------------------
// AliImageParameters.PromptExtendValue
// ---------------------------------------------------------------------

func TestPromptExtendValue(t *testing.T) {
	var nilParams *AliImageParameters
	if nilParams.PromptExtendValue() != false {
		t.Error("nil receiver should return false")
	}

	unset := &AliImageParameters{}
	if unset.PromptExtendValue() != false {
		t.Error("unset PromptExtend should return false")
	}

	trueVal := true
	set := &AliImageParameters{PromptExtend: &trueVal}
	if set.PromptExtendValue() != true {
		t.Error("set PromptExtend=true should return true")
	}
}

// ---------------------------------------------------------------------
// AliOutput image conversion helpers (dto.go)
// ---------------------------------------------------------------------

func TestChoicesToOpenAIImageDate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	t.Run("empty choices returns nil", func(t *testing.T) {
		o := &AliOutput{}
		got := o.ChoicesToOpenAIImageDate(c, "url")
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("non-http image content is used directly as b64", func(t *testing.T) {
		o := &AliOutput{
			Choices: []struct {
				FinishReason string `json:"finish_reason,omitempty"`
				Message      struct {
					Role             string            `json:"role,omitempty"`
					Content          []AliMediaContent `json:"content,omitempty"`
					ReasoningContent string            `json:"reasoning_content,omitempty"`
				} `json:"message,omitempty"`
			}{
				{},
			},
		}
		o.Choices[0].Message.Content = []AliMediaContent{{Image: "aGVsbG8="}}
		got := o.ChoicesToOpenAIImageDate(c, "b64_json")
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1", len(got))
		}
		if got[0].B64Json != "aGVsbG8=" {
			t.Errorf("B64Json = %q, want %q", got[0].B64Json, "aGVsbG8=")
		}
	})

	t.Run("http image url without b64_json format keeps url only", func(t *testing.T) {
		o := &AliOutput{
			Choices: []struct {
				FinishReason string `json:"finish_reason,omitempty"`
				Message      struct {
					Role             string            `json:"role,omitempty"`
					Content          []AliMediaContent `json:"content,omitempty"`
					ReasoningContent string            `json:"reasoning_content,omitempty"`
				} `json:"message,omitempty"`
			}{
				{},
			},
		}
		o.Choices[0].Message.Content = []AliMediaContent{{Image: "http://example.com/x.png"}}
		got := o.ChoicesToOpenAIImageDate(c, "url")
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1", len(got))
		}
		if got[0].Url != "http://example.com/x.png" {
			t.Errorf("Url = %q, want %q", got[0].Url, "http://example.com/x.png")
		}
		if got[0].B64Json != "" {
			t.Errorf("B64Json = %q, want empty (format != b64_json)", got[0].B64Json)
		}
	})

	t.Run("text content sets RevisedPrompt", func(t *testing.T) {
		o := &AliOutput{
			Choices: []struct {
				FinishReason string `json:"finish_reason,omitempty"`
				Message      struct {
					Role             string            `json:"role,omitempty"`
					Content          []AliMediaContent `json:"content,omitempty"`
					ReasoningContent string            `json:"reasoning_content,omitempty"`
				} `json:"message,omitempty"`
			}{
				{},
			},
		}
		o.Choices[0].Message.Content = []AliMediaContent{{Text: "a red fox"}}
		got := o.ChoicesToOpenAIImageDate(c, "url")
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1", len(got))
		}
		if got[0].RevisedPrompt != "a red fox" {
			t.Errorf("RevisedPrompt = %q, want %q", got[0].RevisedPrompt, "a red fox")
		}
	})
}

func TestResultToOpenAIImageDate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	o := &AliOutput{
		Results: []TaskResult{
			{Url: "http://example.com/a.png", B64Image: "b64data"},
		},
	}
	got := o.ResultToOpenAIImageDate(c, "url")
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Url != "http://example.com/a.png" {
		t.Errorf("Url = %q, want %q", got[0].Url, "http://example.com/a.png")
	}
	if got[0].B64Json != "b64data" {
		t.Errorf("B64Json = %q, want %q (non-b64_json format falls back to B64Image)", got[0].B64Json, "b64data")
	}
}

// ---------------------------------------------------------------------
// responseAli2OpenAIImage
// ---------------------------------------------------------------------

func TestResponseAli2OpenAIImage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{}

	t.Run("prefers Results over Choices", func(t *testing.T) {
		resp := &AliResponse{
			Output: AliOutput{
				Results: []TaskResult{{B64Image: "abc"}},
			},
		}
		got := responseAli2OpenAIImage(c, resp, []byte("orig"), info, "url")
		if len(got.Data) != 1 {
			t.Fatalf("len = %d, want 1", len(got.Data))
		}
		if string(got.Metadata) != "orig" {
			t.Errorf("Metadata = %q, want %q", got.Metadata, "orig")
		}
	})

	t.Run("no results or choices yields nil data", func(t *testing.T) {
		resp := &AliResponse{}
		got := responseAli2OpenAIImage(c, resp, []byte("x"), info, "url")
		if got.Data != nil {
			t.Errorf("expected nil Data, got %v", got.Data)
		}
	})
}

// ---------------------------------------------------------------------
// aliImageHandler (hermetic: sync-model path only; async requires
// asyncTaskWait which performs live HTTP + time.Sleep and needs a live
// upstream, so it is intentionally NOT exercised here.)
// ---------------------------------------------------------------------

type errReadCloser struct{}

func (errReadCloser) Read(p []byte) (int, error) { return 0, errors.New("boom") }
func (errReadCloser) Close() error               { return nil }

func TestAliImageHandler_SyncModelSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Set("response_format", "url")

	body := `{"output":{"results":[{"url":"http://x/1.png"}]},"usage":{"image_count":1}}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
	info := &relaycommon.RelayInfo{}
	a := &Adaptor{IsSyncImageModel: true}

	apiErr, usage := aliImageHandler(a, c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Err)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if info.PriceData.OtherRatios["n"] != 1 {
		t.Errorf("PriceData n ratio = %v, want 1", info.PriceData.OtherRatios["n"])
	}
}

func TestAliImageHandler_MessageFieldReturnsError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	body := `{"message":"quota exceeded","code":"Throttling"}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
	info := &relaycommon.RelayInfo{}
	a := &Adaptor{IsSyncImageModel: true}

	apiErr, usage := aliImageHandler(a, c, resp, info)
	if apiErr == nil {
		t.Fatal("expected error for non-empty Message field")
	}
	if apiErr.Err == nil || apiErr.Err.Error() != "quota exceeded" {
		t.Errorf("error = %v, want %q", apiErr.Err, "quota exceeded")
	}
	if usage != nil {
		t.Errorf("expected nil usage, got %v", usage)
	}
}

func TestAliImageHandler_BadJSONReturnsError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString("not-json")),
	}
	info := &relaycommon.RelayInfo{}
	a := &Adaptor{IsSyncImageModel: true}

	apiErr, usage := aliImageHandler(a, c, resp, info)
	if apiErr == nil {
		t.Fatal("expected error for malformed JSON body")
	}
	if usage != nil {
		t.Errorf("expected nil usage, got %v", usage)
	}
}

func TestAliImageHandler_BodyReadError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       errReadCloser{},
	}
	info := &relaycommon.RelayInfo{}
	a := &Adaptor{IsSyncImageModel: true}

	apiErr, usage := aliImageHandler(a, c, resp, info)
	if apiErr == nil {
		t.Fatal("expected error for body read failure")
	}
	if usage != nil {
		t.Errorf("expected nil usage, got %v", usage)
	}
}

// ---------------------------------------------------------------------
// RerankHandler / ConvertRerankRequest (rerank.go)
// ---------------------------------------------------------------------

func TestConvertRerankRequest_DefaultsReturnDocumentsTrue(t *testing.T) {
	req := dto.RerankRequest{Model: "gte-rerank-v2", Query: "q", Documents: []any{"a"}, TopN: 3}
	got := ConvertRerankRequest(req)
	if got.Parameters.ReturnDocuments == nil || *got.Parameters.ReturnDocuments != true {
		t.Errorf("ReturnDocuments = %v, want true (default)", got.Parameters.ReturnDocuments)
	}
	if got.Parameters.TopN == nil || *got.Parameters.TopN != 3 {
		t.Errorf("TopN = %v, want 3", got.Parameters.TopN)
	}
}

func TestRerankHandler_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	body := `{"output":{"results":[]},"usage":{"total_tokens":42},"request_id":"req-1"}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
	info := &relaycommon.RelayInfo{}

	apiErr, usage := RerankHandler(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Err)
	}
	if usage == nil || usage.TotalTokens != 42 {
		t.Errorf("usage = %+v, want TotalTokens=42", usage)
	}
	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want 200", w.Code)
	}
}

func TestRerankHandler_ErrorCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	body := `{"code":"InvalidParameter","message":"bad input","request_id":"req-2"}`
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
	info := &relaycommon.RelayInfo{}

	apiErr, usage := RerankHandler(c, resp, info)
	if apiErr == nil {
		t.Fatal("expected error for non-empty response code")
	}
	if usage != nil {
		t.Errorf("expected nil usage, got %v", usage)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want 400", apiErr.StatusCode)
	}
}

func TestRerankHandler_BadJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString("not-json")),
	}
	info := &relaycommon.RelayInfo{}

	apiErr, usage := RerankHandler(c, resp, info)
	if apiErr == nil {
		t.Fatal("expected error for malformed JSON body")
	}
	if usage != nil {
		t.Errorf("expected nil usage, got %v", usage)
	}
}

func TestRerankHandler_BodyReadError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       errReadCloser{},
	}
	info := &relaycommon.RelayInfo{}

	apiErr, usage := RerankHandler(c, resp, info)
	if apiErr == nil {
		t.Fatal("expected error for body read failure")
	}
	if usage != nil {
		t.Errorf("expected nil usage, got %v", usage)
	}
}

// ---------------------------------------------------------------------
// DoResponse dispatch (rerank / image-generations branches only; the
// default branch delegates to openai.Adaptor.DoResponse and the claude
// branch to claude.ClaudeHandler/ClaudeStreamHandler against a live-shaped
// upstream response, and DoRequest performs a real outbound HTTP call via
// provider.DoApiRequest -- none of those are hermetically exercisable
// without a live/mocked upstream, so they are intentionally left uncovered
// here per the "no network" constraint.)
// ---------------------------------------------------------------------

func TestDoResponse_RerankDispatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	body := `{"output":{"results":[]},"usage":{"total_tokens":7},"request_id":"req-3"}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeRerank}
	a := &Adaptor{}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Err)
	}
	u, ok := usage.(*dto.Usage)
	if !ok || u.TotalTokens != 7 {
		t.Errorf("usage = %+v (%T), want TotalTokens=7", usage, usage)
	}
}

func TestDoResponse_ImageGenerationsDispatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Set("response_format", "url")

	body := `{"output":{"results":[{"url":"http://x/1.png"}]},"usage":{"image_count":1}}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeImagesGenerations}
	a := &Adaptor{IsSyncImageModel: true}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Err)
	}
	if usage == nil {
		t.Error("expected non-nil usage")
	}
}
