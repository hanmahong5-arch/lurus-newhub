package openai

// Business-acceptance tests for the pure/near-pure Adaptor methods: URL
// construction, header construction, image MIME sniffing, model-list /
// channel-name resolution per channel type, and the Responses-API reasoning
// suffix rewrite. These are the glue that decides which upstream URL and
// credentials every relayed request actually goes to, so a wrong branch here
// silently sends traffic (or leaks keys) to the wrong place.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func newCovTestContext() *gin.Context {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

// ---------------------------------------------------------------------------
// GetRequestURL
// ---------------------------------------------------------------------------

func TestAdaptor_GetRequestURL(t *testing.T) {
	a := &Adaptor{}

	t.Run("default openai channel appends path to base url", func(t *testing.T) {
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
		if url != "https://api.openai.com/v1/chat/completions" {
			t.Errorf("url = %q, want %q", url, "https://api.openai.com/v1/chat/completions")
		}
	})

	t.Run("base url with trailing slash is not deduplicated (documents current behavior)", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:    constant.ChannelTypeOpenAI,
				ChannelBaseUrl: "https://api.openai.com/",
			},
			RequestURLPath: "/v1/chat/completions",
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// GetFullRequestURL is a naive string concat: base + path. A trailing
		// slash on the base therefore produces a double slash. This is a real
		// business edge case (channel base_url entered with trailing slash).
		if url != "https://api.openai.com//v1/chat/completions" {
			t.Errorf("url = %q, want double-slash artifact %q", url, "https://api.openai.com//v1/chat/completions")
		}
	})

	t.Run("custom channel substitutes {model} placeholder", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:       constant.ChannelTypeCustom,
				ChannelBaseUrl:    "https://gw.example.com/{model}/invoke",
				UpstreamModelName: "gpt-4o",
			},
			RequestURLPath: "/v1/chat/completions",
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://gw.example.com/gpt-4o/invoke"
		if url != want {
			t.Errorf("url = %q, want %q", url, want)
		}
	})

	t.Run("azure uses default api version when unset and dedups double dots pre-cutoff", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:       constant.ChannelTypeAzure,
				ChannelBaseUrl:    "https://myres.openai.azure.com",
				UpstreamModelName: "gpt-4.1-mini",
				ChannelCreateTime: constant.AzureNoRemoveDotTime - 1, // strictly before cutoff -> dots removed
			},
			RequestURLPath: "/v1/chat/completions?foo=bar",
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://myres.openai.azure.com/openai/deployments/gpt-41-mini/chat/completions?api-version=" + constant.AzureDefaultAPIVersion
		if url != want {
			t.Errorf("url = %q, want %q", url, want)
		}
	})

	t.Run("azure keeps dots in model name at/after cutoff", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:       constant.ChannelTypeAzure,
				ChannelBaseUrl:    "https://myres.openai.azure.com",
				UpstreamModelName: "gpt-4.1-mini",
				ApiVersion:        "2024-05-01",
				ChannelCreateTime: constant.AzureNoRemoveDotTime, // at cutoff -> dots preserved
			},
			RequestURLPath: "/v1/chat/completions",
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://myres.openai.azure.com/openai/deployments/gpt-4.1-mini/chat/completions?api-version=2024-05-01"
		if url != want {
			t.Errorf("url = %q, want %q", url, want)
		}
	})

	t.Run("azure claude relay format strips /messages and forces chat/completions", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:       constant.ChannelTypeAzure,
				ChannelBaseUrl:    "https://myres.openai.azure.com",
				UpstreamModelName: "claude-3-5-sonnet",
				ApiVersion:        "2024-05-01",
				ChannelCreateTime: constant.AzureNoRemoveDotTime,
			},
			RequestURLPath: "/v1/messages",
			RelayFormat:    types.RelayFormatClaude,
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://myres.openai.azure.com/openai/deployments/claude-3-5-sonnet/chat/completions?api-version=2024-05-01"
		if url != want {
			t.Errorf("url = %q, want %q", url, want)
		}
	})

	t.Run("azure realtime mode overrides task and base scheme to wss", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:       constant.ChannelTypeAzure,
				ChannelBaseUrl:    "https://myres.openai.azure.com",
				UpstreamModelName: "gpt-4o-realtime-preview",
				ApiVersion:        "2024-10-01",
				ChannelCreateTime: constant.AzureNoRemoveDotTime,
			},
			RequestURLPath: "/v1/chat/completions",
			RelayMode:      relayconstant.RelayModeRealtime,
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "wss://myres.openai.azure.com/openai/realtime?deployment=gpt-4o-realtime-preview&api-version=2024-10-01"
		if url != want {
			t.Errorf("url = %q, want %q", url, want)
		}
		// base url on info itself was rewritten to wss as a side effect
		if info.ChannelBaseUrl != "wss://myres.openai.azure.com" {
			t.Errorf("info.ChannelBaseUrl = %q, want wss rewrite", info.ChannelBaseUrl)
		}
	})

	t.Run("non-azure realtime mode rewrites http scheme to ws", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:       constant.ChannelTypeOpenAI,
				ChannelBaseUrl:    "http://relay.local:8080",
				UpstreamModelName: "gpt-4o-realtime-preview",
			},
			RequestURLPath: "/v1/realtime",
			RelayMode:      relayconstant.RelayModeRealtime,
		}
		_, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.ChannelBaseUrl != "ws://relay.local:8080" {
			t.Errorf("info.ChannelBaseUrl = %q, want ws rewrite", info.ChannelBaseUrl)
		}
	})

	t.Run("azure responses mode uses preview api version by default", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:       constant.ChannelTypeAzure,
				ChannelBaseUrl:    "https://myres.openai.azure.com",
				UpstreamModelName: "gpt-4o",
				ChannelCreateTime: constant.AzureNoRemoveDotTime,
			},
			RequestURLPath: "/v1/responses",
			RelayMode:      relayconstant.RelayModeResponses,
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://myres.openai.azure.com/openai/v1/responses?api-version=preview"
		if url != want {
			t.Errorf("url = %q, want %q", url, want)
		}
	})

	t.Run("azure responses mode on cognitiveservices host uses api version and different subpath", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:       constant.ChannelTypeAzure,
				ChannelBaseUrl:    "https://myres.cognitiveservices.azure.com",
				UpstreamModelName: "gpt-4o",
				ApiVersion:        "2024-08-01",
				ChannelCreateTime: constant.AzureNoRemoveDotTime,
			},
			RequestURLPath: "/v1/responses",
			RelayMode:      relayconstant.RelayModeResponses,
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://myres.cognitiveservices.azure.com/openai/responses?api-version=2024-08-01"
		if url != want {
			t.Errorf("url = %q, want %q", url, want)
		}
	})

	t.Run("azure responses mode honors explicit AzureResponsesVersion override", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:       constant.ChannelTypeAzure,
				ChannelBaseUrl:    "https://myres.openai.azure.com",
				UpstreamModelName: "gpt-4o",
				ChannelCreateTime: constant.AzureNoRemoveDotTime,
				ChannelOtherSettings: dto.ChannelOtherSettings{
					AzureResponsesVersion: "2025-01-01-preview",
				},
			},
			RequestURLPath: "/v1/responses",
			RelayMode:      relayconstant.RelayModeResponses,
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://myres.openai.azure.com/openai/v1/responses?api-version=2025-01-01-preview"
		if url != want {
			t.Errorf("url = %q, want %q", url, want)
		}
	})

	t.Run("default channel claude/gemini relay format uses fixed chat/completions suffix", func(t *testing.T) {
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:    constant.ChannelTypeOpenAI,
				ChannelBaseUrl: "https://api.deepseek.com",
			},
			RequestURLPath: "/v1/messages",
			RelayFormat:    types.RelayFormatClaude,
		}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "https://api.deepseek.com/v1/chat/completions"
		if url != want {
			t.Errorf("url = %q, want %q", url, want)
		}
	})
}

// ---------------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------------

func TestAdaptor_SetupRequestHeader(t *testing.T) {
	a := &Adaptor{}

	t.Run("azure sets api-key header and returns before bearer auth", func(t *testing.T) {
		c := newCovTestContext()
		header := &http.Header{}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeAzure, ApiKey: "azure-key-1"},
		}
		if err := a.SetupRequestHeader(c, header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("api-key"); got != "azure-key-1" {
			t.Errorf("api-key header = %q, want %q", got, "azure-key-1")
		}
		if got := header.Get("Authorization"); got != "" {
			t.Errorf("Authorization header should not be set for azure, got %q", got)
		}
	})

	t.Run("openai channel with organization sets OpenAI-Organization header", func(t *testing.T) {
		c := newCovTestContext()
		header := &http.Header{}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, ApiKey: "sk-1", Organization: "org-42"},
		}
		if err := a.SetupRequestHeader(c, header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("OpenAI-Organization"); got != "org-42" {
			t.Errorf("OpenAI-Organization = %q, want %q", got, "org-42")
		}
		if got := header.Get("Authorization"); got != "Bearer sk-1" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer sk-1")
		}
	})

	t.Run("non-openai channel does not set organization header even if present", func(t *testing.T) {
		c := newCovTestContext()
		header := &http.Header{}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeCustom, ApiKey: "sk-2", Organization: "org-should-not-appear"},
		}
		if err := a.SetupRequestHeader(c, header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("OpenAI-Organization"); got != "" {
			t.Errorf("OpenAI-Organization should be empty for non-openai channel, got %q", got)
		}
	})

	t.Run("openrouter channel sets referer and title headers", func(t *testing.T) {
		c := newCovTestContext()
		header := &http.Header{}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenRouter, ApiKey: "sk-or"},
		}
		if err := a.SetupRequestHeader(c, header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("HTTP-Referer"); got == "" {
			t.Error("HTTP-Referer should be set for openrouter channel")
		}
		if got := header.Get("X-Title"); got == "" {
			t.Error("X-Title should be set for openrouter channel")
		}
	})

	t.Run("realtime mode without client Sec-WebSocket-Protocol falls back to bearer + beta header", func(t *testing.T) {
		c := newCovTestContext()
		header := &http.Header{}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, ApiKey: "sk-rt"},
			RelayMode:   relayconstant.RelayModeRealtime,
		}
		if err := a.SetupRequestHeader(c, header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := header.Get("openai-beta"); got != "realtime=v1" {
			t.Errorf("openai-beta = %q, want realtime=v1", got)
		}
		if got := header.Get("Authorization"); got != "Bearer sk-rt" {
			t.Errorf("Authorization = %q, want Bearer sk-rt", got)
		}
		if got := header.Get("Sec-WebSocket-Protocol"); got != "" {
			t.Errorf("Sec-WebSocket-Protocol should be empty, got %q", got)
		}
	})

	t.Run("realtime mode with client Sec-WebSocket-Protocol embeds api key in protocol list", func(t *testing.T) {
		c := newCovTestContext()
		c.Request.Header.Set("Sec-WebSocket-Protocol", "realtime")
		header := &http.Header{}
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, ApiKey: "sk-rt-2"},
			RelayMode:   relayconstant.RelayModeRealtime,
		}
		if err := a.SetupRequestHeader(c, header, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := header.Get("Sec-WebSocket-Protocol")
		want := "realtime,openai-insecure-api-key.sk-rt-2,openai-beta.realtime-v1"
		if got != want {
			t.Errorf("Sec-WebSocket-Protocol = %q, want %q", got, want)
		}
		// the plain bearer branch must NOT also run in this case
		if got := header.Get("Authorization"); got != "" {
			t.Errorf("Authorization should be unset when ws-protocol path taken, got %q", got)
		}
	})
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
		{"photo.JPG", "image/jpeg"},
		{"photo.jpeg", "image/jpeg"},
		{"photo.png", "image/png"},
		{"photo.webp", "image/webp"},
		{"photo.jp2", "image/jpeg"},  // unmapped but starts with "jp" -> jpeg fallback
		{"photo.gif", "image/png"},   // unknown extension falls back to png
		{"photo", "image/png"},       // no extension at all
		{"photo.PNG", "image/png"},
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
// ConvertOpenAIResponsesRequest
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertOpenAIResponsesRequest(t *testing.T) {
	a := &Adaptor{}
	c := newCovTestContext()

	t.Run("suffix extraction creates new Reasoning object when nil", func(t *testing.T) {
		req := dto.OpenAIResponsesRequest{Model: "o3-mini-high"}
		result, err := a.ConvertOpenAIResponsesRequest(c, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		r := result.(dto.OpenAIResponsesRequest)
		if r.Model != "o3-mini" {
			t.Errorf("Model = %q, want o3-mini", r.Model)
		}
		if r.Reasoning == nil || r.Reasoning.Effort != "high" {
			t.Fatalf("Reasoning = %+v, want Effort=high", r.Reasoning)
		}
	})

	t.Run("suffix extraction overwrites effort on existing Reasoning object", func(t *testing.T) {
		req := dto.OpenAIResponsesRequest{
			Model:     "o3-mini-low",
			Reasoning: &dto.Reasoning{Effort: "medium", Summary: "keep-me"},
		}
		result, err := a.ConvertOpenAIResponsesRequest(c, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		r := result.(dto.OpenAIResponsesRequest)
		if r.Reasoning.Effort != "low" {
			t.Errorf("Effort = %q, want low", r.Reasoning.Effort)
		}
		if r.Reasoning.Summary != "keep-me" {
			t.Errorf("Summary should be preserved, got %q", r.Reasoning.Summary)
		}
	})

	t.Run("no suffix passes model through unchanged", func(t *testing.T) {
		req := dto.OpenAIResponsesRequest{Model: "gpt-4o"}
		result, err := a.ConvertOpenAIResponsesRequest(c, &relaycommon.RelayInfo{}, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		r := result.(dto.OpenAIResponsesRequest)
		if r.Model != "gpt-4o" {
			t.Errorf("Model = %q, want gpt-4o", r.Model)
		}
		if r.Reasoning != nil {
			t.Errorf("Reasoning should stay nil, got %+v", r.Reasoning)
		}
	})
}

// ---------------------------------------------------------------------------
// ConvertRerankRequest / ConvertEmbeddingRequest (pure passthrough)
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertRerankRequest(t *testing.T) {
	a := &Adaptor{}
	c := newCovTestContext()
	req := dto.RerankRequest{Model: "rerank-1", Query: "q", Documents: []any{"a", "b"}}
	result, err := a.ConvertRerankRequest(c, relayconstant.RelayModeRerank, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r, ok := result.(dto.RerankRequest)
	if !ok {
		t.Fatalf("result type = %T, want dto.RerankRequest", result)
	}
	if r.Model != "rerank-1" || r.Query != "q" || len(r.Documents) != 2 {
		t.Errorf("passthrough mutated request: %+v", r)
	}
}

func TestAdaptor_ConvertEmbeddingRequest(t *testing.T) {
	a := &Adaptor{}
	c := newCovTestContext()
	req := dto.EmbeddingRequest{Model: "text-embedding-3-small", Input: "hello"}
	result, err := a.ConvertEmbeddingRequest(c, &relaycommon.RelayInfo{}, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r, ok := result.(dto.EmbeddingRequest)
	if !ok {
		t.Fatalf("result type = %T, want dto.EmbeddingRequest", result)
	}
	if r.Model != "text-embedding-3-small" {
		t.Errorf("passthrough mutated Model: %+v", r)
	}
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName across all supported channel types
// ---------------------------------------------------------------------------

func TestAdaptor_GetModelList_PerChannelType(t *testing.T) {
	tests := []struct {
		name        string
		channelType int
		wantEmpty   bool // openrouter's own ModelList is intentionally empty upstream
	}{
		{"360", constant.ChannelType360, false},
		{"lingyiwanwu", constant.ChannelTypeLingYiWanWu, false},
		{"xinference", constant.ChannelTypeXinference, false},
		{"openrouter", constant.ChannelTypeOpenRouter, true},
		{"default-openai", constant.ChannelTypeOpenAI, false},
	}
	// each channel type must resolve to a *different* underlying model list
	// (proves the switch actually dispatches, not just returns ModelList always)
	seen := map[string][]string{}
	for _, tt := range tests {
		a := &Adaptor{ChannelType: tt.channelType}
		list := a.GetModelList()
		if tt.wantEmpty {
			if len(list) != 0 {
				t.Errorf("%s: GetModelList() = %v, want empty", tt.name, list)
			}
			continue
		}
		if len(list) == 0 {
			t.Errorf("%s: GetModelList() empty", tt.name)
		}
		seen[tt.name] = list
	}
	if seen["360"][0] == seen["default-openai"][0] {
		t.Error("360 and default-openai should resolve different model list contents")
	}
}

func TestAdaptor_GetChannelName_PerChannelType(t *testing.T) {
	tests := []struct {
		channelType int
		want        string
	}{
		{constant.ChannelType360, "ai360"},
		{constant.ChannelTypeLingYiWanWu, "lingyiwanwu"},
		{constant.ChannelTypeXinference, "xinference"},
		{constant.ChannelTypeOpenRouter, "openrouter"},
		{constant.ChannelTypeOpenAI, "openai"},
	}
	for _, tt := range tests {
		a := &Adaptor{ChannelType: tt.channelType}
		if got := a.GetChannelName(); got != tt.want {
			t.Errorf("channelType=%d: GetChannelName() = %q, want %q", tt.channelType, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Init: ThinkingToContent side effect
// ---------------------------------------------------------------------------

func TestAdaptor_Init_ThinkingToContentSetup(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    constant.ChannelTypeOpenAI,
			ChannelSetting: dto.ChannelSettings{ThinkingToContent: true},
		},
	}
	a.Init(info)
	if !info.IsFirstThinkingContent {
		t.Error("IsFirstThinkingContent should be true after Init with ThinkingToContent enabled")
	}
	if info.SendLastThinkingContent {
		t.Error("SendLastThinkingContent should start false")
	}
	if info.HasSentThinkingContent {
		t.Error("HasSentThinkingContent should start false")
	}
}

func TestAdaptor_Init_WithoutThinkingToContent(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
	}
	a.Init(info)
	// zero-value ThinkingContentInfo: IsFirstThinkingContent must stay false
	// since the Init code path that flips it on was never entered.
	if info.IsFirstThinkingContent {
		t.Error("IsFirstThinkingContent should remain false when ThinkingToContent disabled")
	}
}
