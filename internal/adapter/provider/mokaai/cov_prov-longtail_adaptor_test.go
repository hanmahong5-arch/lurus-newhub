package mokaai

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

func newProvLongtailMokaCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/embeddings", nil)
	return c, w
}

// ---------------------------------------------------------------------------
// GetRequestURL: MokaAI multiplexes chat vs. embeddings off the upstream
// model name prefix "m3e" — get this wrong and embedding requests hit the
// chat endpoint (or vice versa).
// ---------------------------------------------------------------------------

func TestMoka_GetRequestURL_M3ePrefix_UsesEmbeddingsPath(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.mokaai.com"},
	}
	info.UpstreamModelName = "m3e-large"
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.mokaai.com/embeddings" {
		t.Errorf("GetRequestURL = %q, want %q", got, "https://api.mokaai.com/embeddings")
	}
}

func TestMoka_GetRequestURL_NonM3eModel_UsesChatPath(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.mokaai.com"},
	}
	info.UpstreamModelName = "some-chat-model"
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.mokaai.com/chat/" {
		t.Errorf("GetRequestURL = %q, want %q", got, "https://api.mokaai.com/chat/")
	}
}

func TestMoka_GetRequestURL_EmptyModelName_DefaultsToChat(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://x"},
	}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://x/chat/" {
		t.Errorf("GetRequestURL = %q, want chat path by default", got)
	}
}

// ---------------------------------------------------------------------------
// SetupRequestHeader
// ---------------------------------------------------------------------------

func TestMoka_SetupRequestHeader_BearerAuth(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMokaCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "moka-secret"}}
	header := http.Header{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("Authorization"); got != "Bearer moka-secret" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer moka-secret")
	}
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest: only embeddings mode is implemented; everything else
// errors. Nil request must not panic.
// ---------------------------------------------------------------------------

func TestMoka_ConvertOpenAIRequest_NilRequest_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMokaCtx()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeEmbeddings, ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertOpenAIRequest(c, info, nil); err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestMoka_ConvertOpenAIRequest_NonEmbeddingMode_Errors(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMokaCtx()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "m3e-large"}
	if _, err := a.ConvertOpenAIRequest(c, info, req); err == nil {
		t.Fatal("expected error for unimplemented relay mode (mokaai only implements embeddings)")
	}
}

func TestMoka_ConvertOpenAIRequest_Embeddings_StringInput(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMokaCtx()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeEmbeddings, ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "m3e-large", Input: "hello"}
	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := got.(*dto.EmbeddingRequest)
	inputSlice, ok := out.Input.([]string)
	if !ok || len(inputSlice) != 1 || inputSlice[0] != "hello" {
		t.Errorf("Input = %#v, want []string{\"hello\"} (single string wrapped)", out.Input)
	}
	if out.Model != "m3e-large" {
		t.Errorf("Model = %q, want m3e-large", out.Model)
	}
}

func TestMoka_ConvertOpenAIRequest_Embeddings_StringSliceInput(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMokaCtx()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeEmbeddings, ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "m3e-base", Input: []string{"a", "b"}}
	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	inputSlice := got.(*dto.EmbeddingRequest).Input.([]string)
	if len(inputSlice) != 2 || inputSlice[0] != "a" || inputSlice[1] != "b" {
		t.Errorf("Input = %#v, want [a b] passed through as-is", inputSlice)
	}
}

func TestMoka_ConvertOpenAIRequest_Embeddings_InterfaceSliceInput_ExtractsStrings(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMokaCtx()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeEmbeddings, ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "m3e-base", Input: []interface{}{"a", 42, "b"}}
	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	inputSlice := got.(*dto.EmbeddingRequest).Input.([]string)
	if len(inputSlice) != 2 || inputSlice[0] != "a" || inputSlice[1] != "b" {
		t.Errorf("Input = %#v, want only the string entries extracted ([a b]), non-strings silently dropped", inputSlice)
	}
}

func TestMoka_ConvertOpenAIRequest_Embeddings_UnsupportedInputType_ProducesEmptyInput(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMokaCtx()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeEmbeddings, ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "m3e-base", Input: 12345}
	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// An unrecognized (non-string, non-slice) Input type falls through the
	// type switch untouched: the declared []string zero value (nil) is what
	// gets carried into the request — not a panic, and not silently
	// converted to some string representation of the number.
	inputSlice, ok := got.(*dto.EmbeddingRequest).Input.([]string)
	if !ok {
		t.Fatalf("Input = %#v (%T), want a []string-typed value (possibly nil/empty)", got.(*dto.EmbeddingRequest).Input, got.(*dto.EmbeddingRequest).Input)
	}
	if len(inputSlice) != 0 {
		t.Errorf("Input = %#v, want empty for an unrecognized input type", inputSlice)
	}
}

// ---------------------------------------------------------------------------
// ConvertEmbeddingRequest / ConvertRerankRequest surfaces.
// ---------------------------------------------------------------------------

func TestMoka_ConvertEmbeddingRequest_PassThrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMokaCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := dto.EmbeddingRequest{Model: "m3e-small"}
	got, err := a.ConvertEmbeddingRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.(dto.EmbeddingRequest).Model != "m3e-small" {
		t.Errorf("got %#v, want pass-through", got)
	}
}

func TestMoka_ConvertRerankRequest_UnsupportedNilNil(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMokaCtx()
	got, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
	if err != nil || got != nil {
		t.Errorf("ConvertRerankRequest = (%v, %v), want (nil, nil) — mokaai has no rerank support", got, err)
	}
}

func TestMoka_ModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	if a.GetChannelName() != "mokaai" {
		t.Errorf("GetChannelName() = %q, want mokaai", a.GetChannelName())
	}
	list := a.GetModelList()
	if len(list) != 3 {
		t.Fatalf("GetModelList() len = %d, want 3", len(list))
	}
}

// ---------------------------------------------------------------------------
// DoResponse dispatch: only embeddings mode routes to a real handler; every
// other relay mode is a documented no-op (returns nil usage, nil error) —
// lock this so a future accidental panic on unhandled modes is caught.
// ---------------------------------------------------------------------------

func TestMoka_DoResponse_NonEmbeddingMode_ReturnsNilNil(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailMokaCtx()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{}}
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil for unhandled relay mode (documented no-op)", usage)
	}
}

func TestMoka_DoResponse_EmbeddingMode_DelegatesToHandler(t *testing.T) {
	a := &Adaptor{}
	c, w := newProvLongtailMokaCtx()
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeEmbeddings, ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],"model":"m3e","usage":{"prompt_tokens":3,"total_tokens":3}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage, got %T", usage)
	}
	if u.TotalTokens != 3 {
		t.Errorf("TotalTokens = %d, want 3 (billed token count from upstream usage)", u.TotalTokens)
	}
	if w.Code != 200 {
		t.Errorf("status = %d, want 200 forwarded", w.Code)
	}
}

// ---------------------------------------------------------------------------
// mokaEmbeddingHandler: usage extraction is the money-adjacent surface;
// malformed JSON and empty bodies must error cleanly, not panic.
// ---------------------------------------------------------------------------

func TestMoka_EmbeddingHandler_UsageAndDataMapping(t *testing.T) {
	c, w := newProvLongtailMokaCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[1.0,2.0,3.0]}],"model":"m3e-large","usage":{"prompt_tokens":7,"total_tokens":7}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
	usage, apiErr := mokaEmbeddingHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage.PromptTokens != 7 || usage.TotalTokens != 7 {
		t.Errorf("usage = %+v, want PromptTokens=7 TotalTokens=7 (billed amount from upstream)", usage)
	}
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"embedding":[1,2,3]`) {
		t.Errorf("body = %q, want embedding vector preserved (data conversion must not drop/truncate)", w.Body.String())
	}
}

func TestMoka_EmbeddingHandler_MalformedJSON_Errors(t *testing.T) {
	c, _ := newProvLongtailMokaCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("not json")), Header: make(http.Header)}
	usage, apiErr := mokaEmbeddingHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error for malformed upstream body")
	}
	if usage != nil {
		t.Errorf("usage should be nil on error, got %v", usage)
	}
}

func TestMoka_EmbeddingHandler_EmptyDataArray_NoUsageFabrication(t *testing.T) {
	c, _ := newProvLongtailMokaCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"object":"list","data":[],"model":"m3e","usage":{"prompt_tokens":0,"total_tokens":0}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
	usage, apiErr := mokaEmbeddingHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage.TotalTokens != 0 {
		t.Errorf("TotalTokens = %d, want 0 (no data must not fabricate a nonzero billed amount)", usage.TotalTokens)
	}
}
