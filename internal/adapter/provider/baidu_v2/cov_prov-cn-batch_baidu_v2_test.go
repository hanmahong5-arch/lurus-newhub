package baidu_v2

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func prov_cn_batch_baiduv2GinContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v2/chat/completions", nil)
	return c, w
}

// ---------------------------------------------------------------------------
// Unimplemented conversion paths must fail loudly, not silently no-op.
// ---------------------------------------------------------------------------

func TestAdaptor_UnimplementedConversions_ReturnError(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_cn_batch_baiduv2GinContext()

	t.Run("ConvertGeminiRequest", func(t *testing.T) {
		out, err := a.ConvertGeminiRequest(c, &relaycommon.RelayInfo{}, &dto.GeminiChatRequest{})
		if err == nil || out != nil {
			t.Errorf("expected (nil, error) for unimplemented Gemini conversion, got (%v, %v)", out, err)
		}
	})

	t.Run("ConvertAudioRequest", func(t *testing.T) {
		out, err := a.ConvertAudioRequest(c, &relaycommon.RelayInfo{}, dto.AudioRequest{})
		if err == nil || out != nil {
			t.Errorf("expected (nil, error) for unimplemented audio conversion, got (%v, %v)", out, err)
		}
	})

	t.Run("ConvertImageRequest", func(t *testing.T) {
		out, err := a.ConvertImageRequest(c, &relaycommon.RelayInfo{}, dto.ImageRequest{})
		if err == nil || out != nil {
			t.Errorf("expected (nil, error) for unimplemented image conversion, got (%v, %v)", out, err)
		}
	})

	t.Run("ConvertRerankRequest", func(t *testing.T) {
		out, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
		if err == nil || out != nil {
			t.Errorf("expected (nil, error) for unimplemented rerank conversion, got (%v, %v)", out, err)
		}
	})

	t.Run("ConvertEmbeddingRequest", func(t *testing.T) {
		out, err := a.ConvertEmbeddingRequest(c, &relaycommon.RelayInfo{}, dto.EmbeddingRequest{})
		if err == nil || out != nil {
			t.Errorf("expected (nil, error) for unimplemented embedding conversion, got (%v, %v)", out, err)
		}
	})

	t.Run("ConvertOpenAIResponsesRequest", func(t *testing.T) {
		out, err := a.ConvertOpenAIResponsesRequest(c, &relaycommon.RelayInfo{}, dto.OpenAIResponsesRequest{})
		if err == nil || out != nil {
			t.Errorf("expected (nil, error) for unimplemented responses conversion, got (%v, %v)", out, err)
		}
	})
}

func TestAdaptor_Init_NoOp(t *testing.T) {
	a := &Adaptor{}
	// Init must not panic even with a bare RelayInfo (no ChannelMeta).
	a.Init(&relaycommon.RelayInfo{})
}

// ---------------------------------------------------------------------------
// ConvertClaudeRequest delegates to openai.Adaptor — verifies delegation
// preserves the request rather than dropping/mutating it.
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertClaudeRequest_DelegatesToOpenAIAdaptor(t *testing.T) {
	a := &Adaptor{}
	c, _ := prov_cn_batch_baiduv2GinContext()
	temp := 0.5
	req := &dto.ClaudeRequest{Model: "ernie-4.0-8k", MaxTokens: 128, Temperature: &temp}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	out, err := a.ConvertClaudeRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := out.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("expected delegation to openai.Adaptor to translate into *dto.GeneralOpenAIRequest, got %T", out)
	}
	if got.Model != "ernie-4.0-8k" {
		t.Errorf("Model = %q, want ernie-4.0-8k (translated from Claude request)", got.Model)
	}
	if got.MaxTokens != 128 {
		t.Errorf("MaxTokens = %d, want 128", got.MaxTokens)
	}
}

// ---------------------------------------------------------------------------
// Adaptor.GetRequestURL — v2 endpoint routing by relay mode
// ---------------------------------------------------------------------------

func TestAdaptor_GetRequestURL(t *testing.T) {
	tests := []struct {
		name string
		mode int
		want string
	}{
		{"chat completions", relayconstant.RelayModeChatCompletions, "https://qianfan.baidubce.com/v2/chat/completions"},
		{"embeddings", relayconstant.RelayModeEmbeddings, "https://qianfan.baidubce.com/v2/embeddings"},
		{"image generation", relayconstant.RelayModeImagesGenerations, "https://qianfan.baidubce.com/v2/images/generations"},
		{"image edits", relayconstant.RelayModeImagesEdits, "https://qianfan.baidubce.com/v2/images/edits"},
		{"rerank", relayconstant.RelayModeRerank, "https://qianfan.baidubce.com/v2/rerank"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Adaptor{}
			info := &relaycommon.RelayInfo{RelayMode: tt.mode, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://qianfan.baidubce.com"}}
			url, err := a.GetRequestURL(info)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if url != tt.want {
				t.Errorf("url = %q, want %q", url, tt.want)
			}
		})
	}

	t.Run("unsupported relay mode returns error, not empty string", func(t *testing.T) {
		a := &Adaptor{}
		info := &relaycommon.RelayInfo{RelayMode: 9999, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://qianfan.baidubce.com"}}
		url, err := a.GetRequestURL(info)
		if err == nil {
			t.Fatal("expected error for unsupported relay mode")
		}
		if url != "" {
			t.Errorf("expected empty url on error, got %q", url)
		}
	})

	t.Run("base URL with trailing slash is not de-duplicated (documents current behavior)", func(t *testing.T) {
		a := &Adaptor{}
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://qianfan.baidubce.com/"}}
		url, err := a.GetRequestURL(info)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// FINDING: a trailing-slash base URL produces a double-slash path
		// (".../  /v2/chat/completions") since GetRequestURL does a naive
		// fmt.Sprintf("%s/v2/...", baseUrl) with no TrimSuffix. Locking in the
		// current (buggy-looking but real) behavior as a regression baseline.
		if url != "https://qianfan.baidubce.com//v2/chat/completions" {
			t.Errorf("url = %q, want the double-slash form documenting the untrimmed base URL", url)
		}
	})
}

// ---------------------------------------------------------------------------
// Adaptor.SetupRequestHeader — Authorization + optional appid header
// ---------------------------------------------------------------------------

func TestAdaptor_SetupRequestHeader(t *testing.T) {
	t.Run("single-part key sets bearer auth, no appid header", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_cn_batch_baiduv2GinContext()
		req := &http.Header{}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-onlykey"}}
		if err := a.SetupRequestHeader(c, req, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Get("Authorization") != "Bearer sk-onlykey" {
			t.Errorf("Authorization = %q, want Bearer sk-onlykey", req.Get("Authorization"))
		}
		if req.Get("appid") != "" {
			t.Errorf("expected no appid header for single-part key, got %q", req.Get("appid"))
		}
	})

	t.Run("two-part key sets both Authorization and appid", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_cn_batch_baiduv2GinContext()
		req := &http.Header{}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-onlykey|app-999"}}
		if err := a.SetupRequestHeader(c, req, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Get("Authorization") != "Bearer sk-onlykey" {
			t.Errorf("Authorization = %q, want Bearer sk-onlykey (only the key part, not the whole raw string)", req.Get("Authorization"))
		}
		if req.Get("appid") != "app-999" {
			t.Errorf("appid = %q, want app-999", req.Get("appid"))
		}
	})

	t.Run("two-part key with empty appid segment omits the header", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_cn_batch_baiduv2GinContext()
		req := &http.Header{}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "sk-onlykey|"}}
		if err := a.SetupRequestHeader(c, req, info); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Get("appid") != "" {
			t.Errorf("expected no appid header for empty appid segment, got %q", req.Get("appid"))
		}
	})

	t.Run("empty api key rejected", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_cn_batch_baiduv2GinContext()
		req := &http.Header{}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: ""}}
		err := a.SetupRequestHeader(c, req, info)
		if err == nil {
			t.Fatal("expected error for empty api key")
		}
	})
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertOpenAIRequest — "-search" suffix model rewriting + web_search
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertOpenAIRequest(t *testing.T) {
	t.Run("nil request rejected", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_cn_batch_baiduv2GinContext()
		_, err := a.ConvertOpenAIRequest(c, &relaycommon.RelayInfo{}, nil)
		if err == nil {
			t.Fatal("expected error for nil request")
		}
	})

	t.Run("non-search model name passes through unchanged", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_cn_batch_baiduv2GinContext()
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "ernie-4.0-8k"}}
		req := &dto.GeneralOpenAIRequest{Model: "ernie-4.0-8k"}
		out, err := a.ConvertOpenAIRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != req {
			t.Errorf("expected same request pointer for non-search model, got %+v", out)
		}
		if info.UpstreamModelName != "ernie-4.0-8k" {
			t.Errorf("model name must not be mutated for non-search models, got %q", info.UpstreamModelName)
		}
	})

	t.Run("-search suffix strips suffix, sets model, and injects web_search when caller omitted it", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_cn_batch_baiduv2GinContext()
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "ernie-4.0-8k-search"}}
		req := &dto.GeneralOpenAIRequest{Model: "ernie-4.0-8k-search"}
		out, err := a.ConvertOpenAIRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.UpstreamModelName != "ernie-4.0-8k" {
			t.Errorf("expected -search suffix trimmed from UpstreamModelName, got %q", info.UpstreamModelName)
		}
		if req.Model != "ernie-4.0-8k" {
			t.Errorf("expected -search suffix trimmed from request.Model, got %q", req.Model)
		}
		toMap, ok := out.(map[string]any)
		if !ok {
			t.Fatalf("expected map[string]any output for auto-injected web_search, got %T", out)
		}
		ws, ok := toMap["web_search"].(map[string]any)
		if !ok {
			t.Fatalf("expected web_search key injected into map, got %v", toMap["web_search"])
		}
		if ws["enable"] != true {
			t.Errorf("expected web_search.enable=true, got %v", ws["enable"])
		}
		if ws["enable_status"] != false {
			t.Errorf("expected web_search.enable_status=false, got %v", ws["enable_status"])
		}
	})

	t.Run("-search suffix with caller-supplied web_search is left untouched (not overridden)", func(t *testing.T) {
		a := &Adaptor{}
		c, _ := prov_cn_batch_baiduv2GinContext()
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "ernie-4.0-8k-search"}}
		req := &dto.GeneralOpenAIRequest{
			Model:     "ernie-4.0-8k-search",
			WebSearch: []byte(`{"enable":false}`),
		}
		out, err := a.ConvertOpenAIRequest(c, info, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out != req {
			t.Errorf("caller-supplied web_search must short-circuit auto-injection and return the original request pointer, got %T", out)
		}
	})
}

func TestAdaptor_DoRequest_DelegatesToApiRequest(t *testing.T) {
	// DoRequest is a thin wrapper around provider.DoApiRequest; without a
	// reachable ChannelBaseUrl it must surface an error rather than panic or
	// silently succeed with a nil response.
	a := &Adaptor{}
	c, _ := prov_cn_batch_baiduv2GinContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "http://127.0.0.1:0"}}
	_, err := a.DoRequest(c, info, strings.NewReader("{}"))
	if err == nil {
		t.Fatal("expected an error dialing an unreachable base URL, got nil")
	}
}

// ---------------------------------------------------------------------------
// Adaptor.DoResponse — delegates to openai.Adaptor.DoResponse
// ---------------------------------------------------------------------------

func TestAdaptor_DoResponse_DelegatesToOpenAIHandler(t *testing.T) {
	a := &Adaptor{}
	c, w := prov_cn_batch_baiduv2GinContext()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "ernie-4.0-8k"},
		RelayFormat: "openai",
	}
	body := `{"id":"c1","model":"ernie-4.0-8k","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage, got %T", usage)
	}
	if u.TotalTokens != 5 {
		t.Errorf("TotalTokens = %d, want 5 (usage extracted for billing via delegated openai handler)", u.TotalTokens)
	}
	if !strings.Contains(w.Body.String(), `"total_tokens":5`) {
		t.Errorf("response body should be forwarded to client, got %q", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestAdaptor_GetModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	models := a.GetModelList()
	found := false
	for _, m := range models {
		if m == "ernie-4.0-8k" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected ernie-4.0-8k in supported model list, got %v", models)
	}
	if a.GetChannelName() != ChannelName {
		t.Errorf("channel name = %q, want %q", a.GetChannelName(), ChannelName)
	}
}
