package palm

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

// provLongtailCloseNotifyRecorder adds http.CloseNotifier to
// httptest.ResponseRecorder, which gin.Context.Stream() requires (palm's
// streaming handler uses c.Stream internally).
type provLongtailCloseNotifyRecorder struct {
	*httptest.ResponseRecorder
}

func (r *provLongtailCloseNotifyRecorder) CloseNotify() <-chan bool {
	return make(chan bool)
}

func newProvLongtailPalmCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	w := &provLongtailCloseNotifyRecorder{rec}
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c, rec
}

// ---------------------------------------------------------------------------
// GetRequestURL / SetupRequestHeader
// ---------------------------------------------------------------------------

func TestPalm_GetRequestURL(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://generativelanguage.googleapis.com"}}
	got, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://generativelanguage.googleapis.com/v1beta2/models/chat-bison-001:generateMessage"
	if got != want {
		t.Errorf("GetRequestURL = %q, want %q", got, want)
	}
}

func TestPalm_SetupRequestHeader_GoogApiKey(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailPalmCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "palm-secret"}}
	header := http.Header{}
	if err := a.SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := header.Get("x-goog-api-key"); got != "palm-secret" {
		t.Errorf("x-goog-api-key = %q, want %q", got, "palm-secret")
	}
}

// ---------------------------------------------------------------------------
// ConvertOpenAIRequest
// ---------------------------------------------------------------------------

func TestPalm_ConvertOpenAIRequest_NilRequest(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailPalmCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	if _, err := a.ConvertOpenAIRequest(c, info, nil); err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestPalm_ConvertOpenAIRequest_PassThrough(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailPalmCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	req := &dto.GeneralOpenAIRequest{Model: "PaLM-2"}
	got, err := a.ConvertOpenAIRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != req {
		t.Error("expected the same pointer to pass through untouched")
	}
}

func TestPalm_ConvertRerankRequest_UnsupportedNilNil(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailPalmCtx()
	got, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{})
	if err != nil || got != nil {
		t.Errorf("ConvertRerankRequest = (%v, %v), want (nil, nil): PaLM has no rerank endpoint", got, err)
	}
}

// ---------------------------------------------------------------------------
// GetModelList / GetChannelName
// ---------------------------------------------------------------------------

func TestPalm_ModelListAndChannelName(t *testing.T) {
	a := &Adaptor{}
	if a.GetChannelName() != "google palm" {
		t.Errorf("GetChannelName() = %q, want %q", a.GetChannelName(), "google palm")
	}
	models := a.GetModelList()
	if len(models) != 1 || models[0] != "PaLM-2" {
		t.Errorf("GetModelList() = %v, want [PaLM-2]", models)
	}
}

// ---------------------------------------------------------------------------
// DoResponse (non-stream): candidate -> OpenAI choice mapping, usage
// derivation from candidate text, and the upstream error surface.
// ---------------------------------------------------------------------------

func TestPalm_DoResponse_NonStream_Success(t *testing.T) {
	a := &Adaptor{}
	c, w := newProvLongtailPalmCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "PaLM-2"}}
	body := `{"candidates":[{"author":"1","content":"hello from palm"}]}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage, got %T", usage)
	}
	if u.CompletionTokens <= 0 {
		t.Errorf("CompletionTokens = %d, want >0 for non-empty candidate text (billed quantity)", u.CompletionTokens)
	}
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "hello from palm") {
		t.Errorf("forwarded body missing candidate content, got %q", w.Body.String())
	}
}

func TestPalm_DoResponse_NonStream_NoCandidates_ErrorsWithoutPanic(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailPalmCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	// Empty candidates + no error code: business rule treats this as a
	// failure (guards the candidates[0] index access from panicking).
	body := `{"candidates":[]}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr == nil {
		t.Fatal("expected error when upstream returns zero candidates")
	}
	if u, ok := usage.(*dto.Usage); ok && u != nil {
		t.Errorf("usage should be nil on the no-candidates error path, got %+v", u)
	}
}

func TestPalm_DoResponse_NonStream_UpstreamError(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailPalmCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"error":{"code":429,"message":"rate limited","status":"RESOURCE_EXHAUSTED"}}`
	resp := &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader(body))}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr == nil {
		t.Fatal("expected error for upstream error code")
	}
	if u, ok := usage.(*dto.Usage); ok && u != nil {
		t.Errorf("usage should be nil on upstream error, got %+v", u)
	}
	oaiErr := apiErr.ToOpenAIError()
	if oaiErr.Message != "rate limited" {
		t.Errorf("message = %q, want upstream message propagated", oaiErr.Message)
	}
	if oaiErr.Type != "RESOURCE_EXHAUSTED" {
		t.Errorf("type = %q, want upstream status propagated", oaiErr.Type)
	}
}

func TestPalm_DoResponse_NonStream_MalformedJSON(t *testing.T) {
	a := &Adaptor{}
	c, _ := newProvLongtailPalmCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("not-json{{{"))}
	_, apiErr := a.DoResponse(c, resp, info)
	if apiErr == nil {
		t.Fatal("expected error for malformed body")
	}
}

// ---------------------------------------------------------------------------
// DoResponse (stream): drives palmStreamHandler through the public
// interface. Verifies SSE framing + that the completion text used for usage
// accounting matches the candidate content that was streamed back.
// ---------------------------------------------------------------------------

func TestPalm_DoResponse_Stream_Success(t *testing.T) {
	a := &Adaptor{}
	c, w := newProvLongtailPalmCtx()
	info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "PaLM-2"}}
	body := `{"candidates":[{"author":"1","content":"streamed palm text"}]}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage, got %T", usage)
	}
	if u.CompletionTokens <= 0 {
		t.Errorf("CompletionTokens = %d, want >0 (billed against the streamed candidate text)", u.CompletionTokens)
	}
	if !strings.Contains(w.Body.String(), "streamed palm text") {
		t.Errorf("SSE body missing streamed content, got %q", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "[DONE]") {
		t.Errorf("SSE stream must terminate with [DONE], got %q", w.Body.String())
	}
}

func TestPalm_DoResponse_Stream_MalformedBody_EmptyUsage(t *testing.T) {
	a := &Adaptor{}
	c, w := newProvLongtailPalmCtx()
	info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{}}
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("not json"))}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage, got %T", usage)
	}
	if u.CompletionTokens != 0 {
		t.Errorf("CompletionTokens = %d, want 0 when the upstream body could not be parsed (no text to bill)", u.CompletionTokens)
	}
	// Malformed body must not have leaked a candidate frame onto the wire.
	if strings.Contains(w.Body.String(), "streamed") {
		t.Errorf("unexpected candidate content in body: %q", w.Body.String())
	}
}
