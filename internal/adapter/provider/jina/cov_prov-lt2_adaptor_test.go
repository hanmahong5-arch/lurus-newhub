package jina

// Business-acceptance tests for the Jina adaptor. No prior coverage in this
// repo. Jina only supports rerank/embeddings — GetRequestURL/DoResponse must
// reject any other relay mode instead of silently misrouting billed traffic,
// and ConvertEmbeddingRequest strips EncodingFormat (Jina rejects it).

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/app"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"

	"github.com/gin-gonic/gin"
)

func newProvLt2JinaCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/rerank", nil)
	return c, w
}

// ---------------------------------------------------------------------------
// GetRequestURL: rerank / embeddings only, everything else is an error —
// getting this wrong would silently misroute billed traffic.
// ---------------------------------------------------------------------------

func TestJina_GetRequestURL_Rerank(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.jina.ai"},
		RelayMode:   relayconstant.RelayModeRerank,
	}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.jina.ai/v1/rerank" {
		t.Errorf("GetRequestURL = %q, want %q", got, "https://api.jina.ai/v1/rerank")
	}
}

func TestJina_GetRequestURL_Embeddings(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.jina.ai"},
		RelayMode:   relayconstant.RelayModeEmbeddings,
	}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.jina.ai/v1/embeddings" {
		t.Errorf("GetRequestURL = %q, want %q", got, "https://api.jina.ai/v1/embeddings")
	}
}

func TestJina_GetRequestURL_UnsupportedRelayMode_Errors(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.jina.ai"},
		RelayMode:   relayconstant.RelayModeChatCompletions,
	}
	got, err := a.GetRequestURL(info)
	if err == nil {
		t.Fatal("expected error for a relay mode Jina does not support (chat completions)")
	}
	if got != "" {
		t.Errorf("got = %q, want empty on error", got)
	}
}

// ---------------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------------

func TestJina_SetupRequestHeader_BearerAuth(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2JinaCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "jina-secret"}}
	header := http.Header{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Authorization"); got != "Bearer jina-secret" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer jina-secret")
	}
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest / ConvertRerankRequest: pass-through contracts.
// ---------------------------------------------------------------------------

func TestJina_ConvertOpenAIRequest_Passthrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2JinaCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "jina-clip-v1"}
	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != req {
		t.Error("ConvertOpenAIRequest must return the same request pointer unmodified")
	}
}

func TestJina_ConvertRerankRequest_Passthrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2JinaCtx()
	req := dto.RerankRequest{Model: "jina-reranker-v2-base-multilingual", Query: "q"}
	got, err := a.ConvertRerankRequest(c, 0, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, ok := got.(dto.RerankRequest)
	if !ok || out.Query != "q" {
		t.Errorf("got = %#v, want passthrough of the rerank request", got)
	}
}

// ---------------------------------------------------------------------------
// ConvertEmbeddingRequest: Jina rejects encoding_format, so it must be
// stripped, not just passed through.
// ---------------------------------------------------------------------------

func TestJina_ConvertEmbeddingRequest_StripsEncodingFormat(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2JinaCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.EmbeddingRequest{Model: "jina-clip-v1", Input: "hello", EncodingFormat: "float"}
	got, err := a.ConvertEmbeddingRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := got.(dto.EmbeddingRequest)
	if out.EncodingFormat != "" {
		t.Errorf("EncodingFormat = %q, want stripped to empty (Jina rejects this field)", out.EncodingFormat)
	}
	if out.Model != "jina-clip-v1" || out.Input != "hello" {
		t.Errorf("other fields must survive: %+v", out)
	}
}

// ---------------------------------------------------------------------------
// DoRequest: delegates to provider.DoApiRequest against the channel base URL.
// ---------------------------------------------------------------------------

func TestJina_DoRequest_DelegatesToApiRequest(t *testing.T) {
	app.InitHttpClient()
	fs := system_setting.GetFetchSetting()
	prevAllow := fs.AllowPrivateIp
	fs.AllowPrivateIp = true
	defer func() { system_setting.GetFetchSetting().AllowPrivateIp = prevAllow }()

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	a := &Adaptor{}
	c, _ := newProvLt2JinaCtx()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/rerank", nil)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: srv.URL, ApiKey: "jina-key"},
		RequestURLPath: "/v1/rerank",
		RelayMode:      relayconstant.RelayModeRerank,
	}
	result, err := a.DoRequest(c, info, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp, ok := result.(*http.Response)
	if !ok {
		t.Fatalf("result type = %T, want *http.Response", result)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if gotAuth != "Bearer jina-key" {
		t.Errorf("upstream saw Authorization = %q, want Bearer jina-key", gotAuth)
	}
}

// ---------------------------------------------------------------------------
// DoResponse: rerank dispatches to common_handler.RerankHandler, embeddings
// to openai.OpenaiHandler; any other relay mode is a documented no-op.
// ---------------------------------------------------------------------------

func TestJina_DoResponse_Rerank_UsageFromBody(t *testing.T) {
	a := &Adaptor{}
	c, w := newProvLt2JinaCtx()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeRerank, ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"results":[{"index":0,"relevance_score":0.8}],"usage":{"total_tokens":15}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected rerank mode dispatched to *dto.Usage, got %T", usage)
	}
	if u.TotalTokens != 15 {
		t.Errorf("TotalTokens = %d, want 15 (billed amount from upstream usage.total_tokens)", u.TotalTokens)
	}
	if u.PromptTokens != 15 {
		t.Errorf("PromptTokens = %d, want 15 (RerankHandler mirrors TotalTokens into PromptTokens for non-Xinference channels)", u.PromptTokens)
	}
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestJina_DoResponse_Embeddings_DelegatesToOpenAIHandler(t *testing.T) {
	a := &Adaptor{}
	c, w := newProvLt2JinaCtx()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeEmbeddings, ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],"model":"jina-clip-v1","usage":{"prompt_tokens":6,"total_tokens":6}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected embeddings mode dispatched to *dto.Usage, got %T", usage)
	}
	if u.TotalTokens != 6 {
		t.Errorf("TotalTokens = %d, want 6 (billed amount from upstream usage)", u.TotalTokens)
	}
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestJina_DoResponse_UnsupportedRelayMode_ReturnsNilNil(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLt2JinaCtx()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{}}
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil for an unhandled relay mode (documented no-op)", usage)
	}
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestJina_ModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	if a.GetChannelName() != "jina" {
		t.Errorf("GetChannelName() = %q, want jina", a.GetChannelName())
	}
	list := a.GetModelList()
	if len(list) != 3 {
		t.Fatalf("GetModelList() len = %d, want 3", len(list))
	}
}
