package ali

import (
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// ---------------------------------------------------------------------------
// GetRequestURL: URL construction per relay format/mode is the exact seam
// where a wrong URL silently routes billed traffic to the wrong upstream
// endpoint (e.g. rerank traffic hitting the chat endpoint).
// ---------------------------------------------------------------------------

func TestAdaptor_GetRequestURL(t *testing.T) {
	tests := []struct {
		name string
		info *relaycommon.RelayInfo
		want string
	}{
		{
			name: "claude format uses claude-code-proxy path regardless of relay mode",
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
			name: "images generations sync model routes to multimodal-generation",
			info: &relaycommon.RelayInfo{
				RelayMode:       constant.RelayModeImagesGenerations,
				OriginModelName: "qwen-image-plus",
				ChannelMeta:     &relaycommon.ChannelMeta{ChannelBaseUrl: "https://dashscope.aliyuncs.com"},
			},
			want: "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation",
		},
		{
			name: "images generations async model routes to text2image",
			info: &relaycommon.RelayInfo{
				RelayMode:       constant.RelayModeImagesGenerations,
				OriginModelName: "wanx-v1",
				ChannelMeta:     &relaycommon.ChannelMeta{ChannelBaseUrl: "https://dashscope.aliyuncs.com"},
			},
			want: "https://dashscope.aliyuncs.com/api/v1/services/aigc/text2image/image-synthesis",
		},
		{
			name: "images edits old wan model routes to image2image",
			info: &relaycommon.RelayInfo{
				RelayMode:       constant.RelayModeImagesEdits,
				OriginModelName: "wanx-edit",
				ChannelMeta:     &relaycommon.ChannelMeta{ChannelBaseUrl: "https://dashscope.aliyuncs.com"},
			},
			want: "https://dashscope.aliyuncs.com/api/v1/services/aigc/image2image/image-synthesis",
		},
		{
			name: "images edits new wan2.6 model routes to image-generation",
			info: &relaycommon.RelayInfo{
				RelayMode:       constant.RelayModeImagesEdits,
				OriginModelName: "wan2.6-edit",
				ChannelMeta:     &relaycommon.ChannelMeta{ChannelBaseUrl: "https://dashscope.aliyuncs.com"},
			},
			want: "https://dashscope.aliyuncs.com/api/v1/services/aigc/image-generation/generation",
		},
		{
			name: "images edits non-wan model routes to multimodal-generation",
			info: &relaycommon.RelayInfo{
				RelayMode:       constant.RelayModeImagesEdits,
				OriginModelName: "qwen-image-edit",
				ChannelMeta:     &relaycommon.ChannelMeta{ChannelBaseUrl: "https://dashscope.aliyuncs.com"},
			},
			want: "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation",
		},
		{
			name: "legacy completions",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://dashscope.aliyuncs.com"},
			},
			want: "https://dashscope.aliyuncs.com/compatible-mode/v1/completions",
		},
		{
			name: "default chat completions",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://dashscope.aliyuncs.com"},
			},
			want: "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions",
		},
		{
			name: "base url with trailing slash is preserved verbatim (no double-slash normalization)",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://dashscope.aliyuncs.com/"},
			},
			want: "https://dashscope.aliyuncs.com//compatible-mode/v1/chat/completions",
		},
		{
			name: "base url with existing path suffix is concatenated as-is",
			info: &relaycommon.RelayInfo{
				RelayMode:   constant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://proxy.example.com/ali-proxy"},
			},
			want: "https://proxy.example.com/ali-proxy/compatible-mode/v1/chat/completions",
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

// ---------------------------------------------------------------------------
// SetupRequestHeader: authorization + DashScope-specific headers gate which
// upstream features (SSE, async task mode, plugin routing) are activated.
// ---------------------------------------------------------------------------

func TestAdaptor_SetupRequestHeader(t *testing.T) {
	t.Run("stream sets X-DashScope-SSE and bearer auth", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-secret"}}
		header := http.Header{}
		a := &Adaptor{}
		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("Authorization"); got != "Bearer sk-secret" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer sk-secret")
		}
		if got := header.Get("X-DashScope-SSE"); got != "enable" {
			t.Errorf("X-DashScope-SSE = %q, want %q", got, "enable")
		}
	})

	t.Run("non-stream omits SSE header", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{IsStream: false, ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-secret"}}
		header := http.Header{}
		a := &Adaptor{}
		if err := a.SetupRequestHeader(c, &header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("X-DashScope-SSE"); got != "" {
			t.Errorf("X-DashScope-SSE should be unset for non-stream, got %q", got)
		}
	})

	t.Run("plugin context value forwarded as X-DashScope-Plugin", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		c.Set("plugin", "web-search")
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "k"}}
		header := http.Header{}
		a := &Adaptor{}
		_ = a.SetupRequestHeader(c, &header, info)
		if got := header.Get("X-DashScope-Plugin"); got != "web-search" {
			t.Errorf("X-DashScope-Plugin = %q, want %q", got, "web-search")
		}
	})

	t.Run("sync image generation model does not request async header", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{
			RelayMode:       constant.RelayModeImagesGenerations,
			OriginModelName: "qwen-image",
			ChannelMeta:     &relaycommon.ChannelMeta{ApiKey: "k"},
		}
		header := http.Header{}
		a := &Adaptor{}
		_ = a.SetupRequestHeader(c, &header, info)
		if got := header.Get("X-DashScope-Async"); got != "" {
			t.Errorf("sync image model should not set X-DashScope-Async, got %q", got)
		}
	})

	t.Run("async image generation model requests async header", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{
			RelayMode:       constant.RelayModeImagesGenerations,
			OriginModelName: "wanx-v1",
			ChannelMeta:     &relaycommon.ChannelMeta{ApiKey: "k"},
		}
		header := http.Header{}
		a := &Adaptor{}
		_ = a.SetupRequestHeader(c, &header, info)
		if got := header.Get("X-DashScope-Async"); got != "enable" {
			t.Errorf("async image model should set X-DashScope-Async=enable, got %q", got)
		}
	})

	t.Run("image edits wan model forces async + json content-type", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{
			RelayMode:       constant.RelayModeImagesEdits,
			OriginModelName: "wanx-edit",
			ChannelMeta:     &relaycommon.ChannelMeta{ApiKey: "k"},
		}
		header := http.Header{}
		a := &Adaptor{}
		_ = a.SetupRequestHeader(c, &header, info)
		if got := header.Get("X-DashScope-Async"); got != "enable" {
			t.Errorf("wan image edit should set X-DashScope-Async=enable, got %q", got)
		}
		if got := header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
	})

	t.Run("image edits non-wan model does not force async but still sets json content-type", func(t *testing.T) {
		c, _ := prov_ali_repl_vertex_newGinContext(t)
		info := &relaycommon.RelayInfo{
			RelayMode:       constant.RelayModeImagesEdits,
			OriginModelName: "qwen-image-edit",
			ChannelMeta:     &relaycommon.ChannelMeta{ApiKey: "k"},
		}
		header := http.Header{}
		a := &Adaptor{}
		_ = a.SetupRequestHeader(c, &header, info)
		if got := header.Get("X-DashScope-Async"); got != "" {
			t.Errorf("non-wan image edit should not force async, got %q", got)
		}
		if got := header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
	})
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest: TopP clamping is a billing-neutral but upstream-
// rejecting-request risk (ali rejects top_p==0 or top_p==1 outright).
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertOpenAIRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_ali_repl_vertex_newGinContext(t)

	t.Run("nil request returns error", func(t *testing.T) {
		_, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, nil)
		if err == nil {
			t.Fatal("expected error for nil request, got nil")
		}
		if err.Error() != "request is nil" {
			t.Errorf("error = %q, want %q", err.Error(), "request is nil")
		}
	})

	tests := []struct {
		name     string
		topP     float64
		wantTopP float64
	}{
		{"top_p at upper bound clamped below 1", 1.0, 0.999},
		{"top_p above upper bound clamped", 2.5, 0.999},
		{"top_p at lower bound clamped above 0", 0.0, 0.001},
		{"top_p negative clamped", -0.5, 0.001},
		{"top_p within range passes through unchanged", 0.7, 0.7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &dto.GeneralOpenAIRequest{Model: "qwen-turbo", TopP: tt.topP}
			result, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got, ok := result.(*dto.GeneralOpenAIRequest)
			if !ok {
				t.Fatalf("result type = %T, want *dto.GeneralOpenAIRequest", result)
			}
			if got.TopP != tt.wantTopP {
				t.Errorf("TopP = %v, want %v", got.TopP, tt.wantTopP)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Passthrough / not-implemented converters: these must not silently succeed
// with a corrupted or empty payload, and passthrough ones must not mutate
// or drop data.
// ---------------------------------------------------------------------------

func TestAdaptor_PassthroughAndUnimplementedConverters(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_ali_repl_vertex_newGinContext(t)

	t.Run("ConvertGeminiRequest not implemented", func(t *testing.T) {
		_, err := a.ConvertGeminiRequest(c, &relaycommon.RelayInfo{}, &dto.GeminiChatRequest{})
		if err == nil {
			t.Fatal("expected not-implemented error, got nil")
		}
	})

	t.Run("ConvertAudioRequest not implemented", func(t *testing.T) {
		_, err := a.ConvertAudioRequest(c, &relaycommon.RelayInfo{}, dto.AudioRequest{})
		if err == nil {
			t.Fatal("expected not-implemented error, got nil")
		}
	})

	t.Run("ConvertOpenAIResponsesRequest not implemented", func(t *testing.T) {
		_, err := a.ConvertOpenAIResponsesRequest(c, &relaycommon.RelayInfo{}, dto.OpenAIResponsesRequest{})
		if err == nil {
			t.Fatal("expected not-implemented error, got nil")
		}
	})

	t.Run("ConvertClaudeRequest passes request through unmodified", func(t *testing.T) {
		req := &dto.ClaudeRequest{Model: "qwen-max"}
		result, err := a.ConvertClaudeRequest(c, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != req {
			t.Errorf("ConvertClaudeRequest should return the same pointer, got %+v", result)
		}
	})

	t.Run("ConvertEmbeddingRequest passes request through unmodified", func(t *testing.T) {
		req := dto.EmbeddingRequest{Model: "text-embedding-v1"}
		result, err := a.ConvertEmbeddingRequest(c, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, ok := result.(dto.EmbeddingRequest)
		if !ok || got.Model != "text-embedding-v1" {
			t.Errorf("ConvertEmbeddingRequest result = %+v, want model preserved", result)
		}
	})
}

// ---------------------------------------------------------------------------
// isSyncImageModel / isOldWanModel / isWanModel: these gate which upstream
// URL and header set is used, so a misclassification routes traffic to the
// wrong endpoint (silent 404/400 from upstream, or wrong billing ratio path).
// ---------------------------------------------------------------------------

func TestModelClassifiers(t *testing.T) {
	syncTests := []struct {
		model string
		want  bool
	}{
		{"z-image-turbo", true},
		{"qwen-image-plus", true},
		{"wan2.6-t2i", true},
		{"wanx-v1", false},
		{"qwen-turbo", false},
		{"", false},
	}
	for _, tt := range syncTests {
		t.Run("isSyncImageModel/"+tt.model, func(t *testing.T) {
			if got := isSyncImageModel(tt.model); got != tt.want {
				t.Errorf("isSyncImageModel(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}

	oldWanTests := []struct {
		model string
		want  bool
	}{
		{"wanx-v1", true},
		{"wan2.6-edit", false}, // excluded explicitly: new wan2.6 line is NOT "old"
		{"qwen-image", false},
	}
	for _, tt := range oldWanTests {
		t.Run("isOldWanModel/"+tt.model, func(t *testing.T) {
			if got := isOldWanModel(tt.model); got != tt.want {
				t.Errorf("isOldWanModel(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}

	wanTests := []struct {
		model string
		want  bool
	}{
		{"wanx-v1", true},
		{"wan2.6-edit", true},
		{"qwen-image", false},
	}
	for _, tt := range wanTests {
		t.Run("isWanModel/"+tt.model, func(t *testing.T) {
			if got := isWanModel(tt.model); got != tt.want {
				t.Errorf("isWanModel(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName: these feed the model catalog surfaced to
// tenants; wrong data means tenants see/select unsupported models.
// ---------------------------------------------------------------------------

func TestAdaptor_ModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	models := a.GetModelList()
	if len(models) == 0 {
		t.Fatal("GetModelList returned empty list")
	}
	found := false
	for _, m := range models {
		if m == "qwen-turbo" {
			found = true
		}
	}
	if !found {
		t.Errorf("GetModelList() = %v, expected to contain qwen-turbo", models)
	}
	if got := a.GetChannelName(); got != "ali" {
		t.Errorf("GetChannelName() = %q, want %q", got, "ali")
	}
}

// ---------------------------------------------------------------------------
// DoResponse dispatch: routes different relay modes to the correct handler.
// Wrong wiring here means e.g. rerank traffic gets parsed as a chat response
// (wrong usage extraction -> wrong billing).
// ---------------------------------------------------------------------------

func TestAdaptor_DoResponse_DispatchesRerankToRerankHandler(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_ali_repl_vertex_newGinContext(t)
	body := `{"output":{"results":[{"index":0,"relevance_score":0.9}]},"usage":{"total_tokens":7}}`
	resp := &http.Response{StatusCode: 200, Body: prov_ali_repl_vertex_nopCloser(body)}
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeRerank}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("usage type = %T, want *dto.Usage", usage)
	}
	if u.TotalTokens != 7 {
		t.Errorf("TotalTokens = %d, want 7 (proves RerankHandler, not the chat/image path, handled this)", u.TotalTokens)
	}
}
