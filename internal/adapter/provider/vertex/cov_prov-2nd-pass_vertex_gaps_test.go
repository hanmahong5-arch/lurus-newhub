package vertex

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	pkgconstant "github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func init() {
	// helper.StreamScannerHandler (used by claude/gemini/openai stream
	// handlers reached via DoResponse) builds a time.NewTicker whose interval
	// is StreamingTimeout*time.Second; only common.InitEnv() (full server
	// bootstrap) sets this normally, so give it a safe default here.
	if pkgconstant.StreamingTimeout <= 0 {
		pkgconstant.StreamingTimeout = 60
	}
}

// prov_2nd_pass_vertex_validPEMBody generates a fresh RSA private key and
// returns it PEM-encoded with the header/footer stripped, matching the shape
// createSignedJWT expects (mirrors the fixture pattern already used in
// cov_prov-ali-repl-vertex_pure_test.go's TestCreateSignedJWT).
func prov_2nd_pass_vertex_validPEMBody(t *testing.T) string {
	t.Helper()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	pemBlock := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	body := strings.ReplaceAll(string(pemBlock), "-----BEGIN PRIVATE KEY-----", "")
	body = strings.ReplaceAll(body, "-----END PRIVATE KEY-----", "")
	return body
}

func prov_2nd_pass_vertex_sseBody(frames ...string) string {
	var b strings.Builder
	for _, f := range frames {
		b.WriteString("data: ")
		b.WriteString(f)
		b.WriteString("\n\n")
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// getAccessToken: the token-acquisition seam behind every ADC-mode (non
// api_key) Vertex request. A wrong cache key or a swallowed transport error
// here silently strands every relay call for that channel with a 401.
//
// The success round-trip through Google's real OAuth token endpoint requires
// live network access (authURL is hardcoded, not injectable) and is
// intentionally NOT exercised here per the "no real external network calls"
// constraint; only the network-free branches (cache hit, bad-credentials,
// and pre-network transport-config errors) are covered.
// ---------------------------------------------------------------------------

func TestProv2ndPass_Vertex_GetAccessToken_CacheHitShortCircuits(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 900001}}
	Cache.SetDefault("access-token-900001", "cached-token-abc")

	token, err := getAccessToken(a, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "cached-token-abc" {
		t.Errorf("token = %q, want the cached value returned without touching the network", token)
	}
}

func TestProv2ndPass_Vertex_GetAccessToken_MultiKeyCacheKeyIsDistinctFromSingleKey(t *testing.T) {
	a := &Adaptor{}
	// Same ChannelId, but multi-key: must use a different cache slot than the
	// single-key channel above, or two API keys on one channel would share
	// (and clobber) each other's access token.
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 900001, ChannelIsMultiKey: true, ChannelMultiKeyIndex: 2}}
	Cache.SetDefault("access-token-900001-2", "cached-token-multikey")

	token, err := getAccessToken(a, info)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "cached-token-multikey" {
		t.Errorf("token = %q, want the multi-key-scoped cached value", token)
	}
}

func TestProv2ndPass_Vertex_GetAccessToken_BadCredentialsErrorsBeforeNetwork(t *testing.T) {
	a := &Adaptor{AccountCredentials: Credentials{ClientEmail: "sa@example.com", PrivateKey: "not-a-valid-pem"}}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 900002}}

	_, err := getAccessToken(a, info)
	if err == nil {
		t.Fatal("expected an error for malformed service-account credentials, got nil")
	}
	if !strings.Contains(err.Error(), "failed to create signed JWT") {
		t.Errorf("error = %v, want wrapped 'failed to create signed JWT'", err)
	}
}

func TestProv2ndPass_Vertex_GetAccessToken_BadProxyErrorsBeforeNetwork(t *testing.T) {
	pemBody := prov_2nd_pass_vertex_validPEMBody(t)
	a := &Adaptor{AccountCredentials: Credentials{ClientEmail: "sa@example.com", PrivateKey: pemBody}}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:      900003,
			ChannelSetting: dto.ChannelSettings{Proxy: "http://[::1"}, // malformed -> url.Parse fails before any dial
		},
	}

	_, err := getAccessToken(a, info)
	if err == nil {
		t.Fatal("expected an error for a malformed proxy URL, got nil")
	}
	if !strings.Contains(err.Error(), "failed to exchange JWT for access token") {
		t.Errorf("error = %v, want wrapped 'failed to exchange JWT for access token'", err)
	}
}

// ---------------------------------------------------------------------------
// exchangeJwtForAccessToken / exchangeJwtForAccessTokenWithProxy: proxy
// transport-config error branch, directly (not just via getAccessToken /
// AcquireAccessToken wrappers).
// ---------------------------------------------------------------------------

func TestProv2ndPass_Vertex_ExchangeJwtForAccessToken_BadProxy(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: dto.ChannelSettings{Proxy: "http://[::1"}}}
	_, err := exchangeJwtForAccessToken("dummy-jwt", info)
	if err == nil {
		t.Fatal("expected an error for a malformed proxy URL, got nil")
	}
	if !strings.Contains(err.Error(), "new proxy http client failed") {
		t.Errorf("error = %v, want wrapped 'new proxy http client failed'", err)
	}
}

func TestProv2ndPass_Vertex_ExchangeJwtForAccessTokenWithProxy_BadProxy(t *testing.T) {
	_, err := exchangeJwtForAccessTokenWithProxy("dummy-jwt", "http://[::1")
	if err == nil {
		t.Fatal("expected an error for a malformed proxy URL, got nil")
	}
	if !strings.Contains(err.Error(), "new proxy http client failed") {
		t.Errorf("error = %v, want wrapped 'new proxy http client failed'", err)
	}
}

// ---------------------------------------------------------------------------
// AcquireAccessToken: the standalone (non-RelayInfo) credential-verification
// entrypoint used by the channel "test connection" flow.
// ---------------------------------------------------------------------------

func TestProv2ndPass_Vertex_AcquireAccessToken(t *testing.T) {
	t.Run("bad credentials errors before any network call", func(t *testing.T) {
		_, err := AcquireAccessToken(Credentials{ClientEmail: "sa@example.com", PrivateKey: "garbage"}, "")
		if err == nil {
			t.Fatal("expected an error for malformed credentials, got nil")
		}
		if !strings.Contains(err.Error(), "failed to create signed JWT") {
			t.Errorf("error = %v, want wrapped 'failed to create signed JWT'", err)
		}
	})

	t.Run("good credentials but malformed proxy errors before any network call", func(t *testing.T) {
		pemBody := prov_2nd_pass_vertex_validPEMBody(t)
		_, err := AcquireAccessToken(Credentials{ClientEmail: "sa@example.com", PrivateKey: pemBody}, "http://[::1")
		if err == nil {
			t.Fatal("expected an error for a malformed proxy URL, got nil")
		}
		if !strings.Contains(err.Error(), "new proxy http client failed") {
			t.Errorf("error = %v, want wrapped 'new proxy http client failed'", err)
		}
	})
}

// ---------------------------------------------------------------------------
// Adaptor.SetupRequestHeader (ADC / service-account mode): the branch not
// covered by the existing api_key-mode tests. Wrong wiring here means every
// ADC-mode Vertex channel fails to authenticate (or leaks a stale/empty
// Authorization header).
// ---------------------------------------------------------------------------

func TestProv2ndPass_Vertex_SetupRequestHeader_ADCMode_CacheHitSetsBearerAuth(t *testing.T) {
	Cache.SetDefault("access-token-900010", "adc-cached-token")
	a := &Adaptor{AccountCredentials: Credentials{ProjectID: "proj-adc"}}
	c, _ := prov_ali_repl_vertex_newGinContext(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:            900010,
			UpstreamModelName:    "gemini-1.5-pro",
			ChannelOtherSettings: dto.ChannelOtherSettings{VertexKeyType: dto.VertexKeyTypeJSON},
		},
	}
	hdr := &http.Header{}
	if err := a.SetupRequestHeader(c, hdr, info); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := hdr.Get("Authorization"); got != "Bearer adc-cached-token" {
		t.Errorf("Authorization = %q, want Bearer adc-cached-token", got)
	}
	if got := hdr.Get("x-goog-user-project"); got != "proj-adc" {
		t.Errorf("x-goog-user-project = %q, want proj-adc", got)
	}
}

func TestProv2ndPass_Vertex_SetupRequestHeader_ADCMode_TokenErrorPropagates(t *testing.T) {
	a := &Adaptor{AccountCredentials: Credentials{ClientEmail: "sa@example.com", PrivateKey: "garbage"}}
	c, _ := prov_ali_repl_vertex_newGinContext(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:            900011,
			UpstreamModelName:    "gemini-1.5-pro",
			ChannelOtherSettings: dto.ChannelOtherSettings{VertexKeyType: dto.VertexKeyTypeJSON},
		},
	}
	hdr := &http.Header{}
	err := a.SetupRequestHeader(c, hdr, info)
	if err == nil {
		t.Fatal("expected SetupRequestHeader to propagate the token-acquisition error, got nil")
	}
	if hdr.Get("Authorization") != "" {
		t.Errorf("Authorization = %q, want unset when token acquisition fails", hdr.Get("Authorization"))
	}
}

// ---------------------------------------------------------------------------
// Adaptor.DoResponse: stream-branch dispatch (IsStream=true). The existing
// cov_ suite only exercises the non-stream branches (plus malformed-body
// error paths); every streaming request mode was previously 0%-covered here.
// ---------------------------------------------------------------------------

func TestProv2ndPass_Vertex_DoResponse_Stream_Claude(t *testing.T) {
	a := &Adaptor{RequestMode: RequestModeClaude}
	c, w := prov_ali_repl_vertex_newGinContext(t)
	body := prov_2nd_pass_vertex_sseBody(
		`{"type":"message_start","message":{"id":"m1","model":"claude-3-opus@20240229","usage":{"input_tokens":3,"output_tokens":0}}}`,
		`{"type":"message_stop"}`,
	)
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: prov_ali_repl_vertex_nopCloser(body)}
	info := &relaycommon.RelayInfo{
		IsStream:    true,
		RelayFormat: types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-3-opus@20240229"},
	}

	_, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if w.Body.Len() == 0 {
		t.Error("expected the claude stream route to forward at least one chunk")
	}
}

func TestProv2ndPass_Vertex_DoResponse_Stream_GeminiChat(t *testing.T) {
	a := &Adaptor{RequestMode: RequestModeGemini}
	c, w := prov_ali_repl_vertex_newGinContext(t)
	body := prov_2nd_pass_vertex_sseBody(
		`{"candidates":[{"content":{"parts":[{"text":"Hel"}]},"index":0}]}`,
		`{"candidates":[{"content":{"parts":[{"text":"lo"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":2,"totalTokenCount":4}}`,
	)
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: prov_ali_repl_vertex_nopCloser(body)}
	info := &relaycommon.RelayInfo{
		IsStream:    true,
		RelayMode:   relayconstant.RelayModeChatCompletions,
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-1.5-pro"},
	}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.(*dto.Usage).PromptTokens != 2 {
		t.Errorf("PromptTokens = %d, want 2", usage.(*dto.Usage).PromptTokens)
	}
	if !strings.Contains(w.Body.String(), "[DONE]") {
		t.Errorf("stream output = %q, want terminal [DONE] marker", w.Body.String())
	}
}

func TestProv2ndPass_Vertex_DoResponse_Stream_GeminiNative(t *testing.T) {
	a := &Adaptor{RequestMode: RequestModeGemini}
	c, w := prov_ali_repl_vertex_newGinContext(t)
	body := prov_2nd_pass_vertex_sseBody(
		`{"candidates":[{"content":{"parts":[{"text":"a"}]},"index":0}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`,
	)
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: prov_ali_repl_vertex_nopCloser(body)}
	info := &relaycommon.RelayInfo{
		IsStream:    true,
		RelayMode:   relayconstant.RelayModeGemini,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-1.5-pro"},
	}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.(*dto.Usage).PromptTokens != 1 {
		t.Errorf("PromptTokens = %d, want 1", usage.(*dto.Usage).PromptTokens)
	}
	if info.SendResponseCount != 1 {
		t.Errorf("SendResponseCount = %d, want 1 (native passthrough forwards raw frames)", info.SendResponseCount)
	}
	_ = w
}

func TestProv2ndPass_Vertex_DoResponse_Stream_Llama(t *testing.T) {
	a := &Adaptor{RequestMode: RequestModeLlama}
	c, w := prov_ali_repl_vertex_newGinContext(t)
	body := `data: {"id":"c1","model":"meta/llama3-405b-instruct-maas","choices":[{"delta":{"content":"Hello "}}]}` + "\n\n" +
		`data: {"id":"c1","model":"meta/llama3-405b-instruct-maas","choices":[{"delta":{"content":"world"},"finish_reason":"stop"}]}` + "\n\n" +
		"data: [DONE]\n\n"
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: prov_ali_repl_vertex_nopCloser(body)}
	info := &relaycommon.RelayInfo{
		IsStream:    true,
		RelayMode:   relayconstant.RelayModeChatCompletions,
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: 1, UpstreamModelName: "meta/llama3-405b-instruct-maas"},
	}
	info.SetEstimatePromptTokens(5)
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.(*dto.Usage).PromptTokens != 5 {
		t.Errorf("PromptTokens = %d, want 5 (estimate baseline)", usage.(*dto.Usage).PromptTokens)
	}
	if !strings.Contains(w.Body.String(), "[DONE]") {
		t.Errorf("stream output = %q, want terminal [DONE] marker", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Adaptor.DoResponse: non-stream success paths not covered by the existing
// malformed-body-only test (TestAdaptor_DoResponse_MalformedBodyNoPanic
// only ever sees an error return, never a populated usage).
// ---------------------------------------------------------------------------

func TestProv2ndPass_Vertex_DoResponse_NonStream_GeminiNative_Success(t *testing.T) {
	a := &Adaptor{RequestMode: RequestModeGemini}
	c, w := prov_ali_repl_vertex_newGinContext(t)
	body := `{"candidates":[{"content":{"parts":[{"text":"native"}]},"index":0,"finishReason":null}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3,"totalTokenCount":10}}`
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: prov_ali_repl_vertex_nopCloser(body)}
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeGemini,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-1.5-pro"},
	}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.(*dto.Usage).PromptTokens != 7 || usage.(*dto.Usage).CompletionTokens != 3 {
		t.Errorf("usage = %+v, want PromptTokens=7 CompletionTokens=3", usage)
	}
	if w.Body.String() != body {
		t.Errorf("body = %q, want unchanged passthrough of upstream bytes", w.Body.String())
	}
}

func TestProv2ndPass_Vertex_DoResponse_NonStream_GeminiChat_Success(t *testing.T) {
	a := &Adaptor{RequestMode: RequestModeGemini}
	c, w := prov_ali_repl_vertex_newGinContext(t)
	body := `{"candidates": [{"content": {"parts":[{"text":"hi there"}]}, "finishReason":"STOP", "index":0}],"usageMetadata": {"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}`
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: prov_ali_repl_vertex_nopCloser(body)}
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeChatCompletions,
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-1.5-pro"},
	}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.(*dto.Usage).PromptTokens != 10 || usage.(*dto.Usage).CompletionTokens != 5 {
		t.Errorf("usage = %+v, want PromptTokens=10 CompletionTokens=5", usage)
	}
	if !strings.Contains(w.Body.String(), "hi there") {
		t.Errorf("body = %q, want the upstream text forwarded", w.Body.String())
	}
}

func TestProv2ndPass_Vertex_DoResponse_NonStream_Llama_Success(t *testing.T) {
	a := &Adaptor{RequestMode: RequestModeLlama}
	c, w := prov_ali_repl_vertex_newGinContext(t)
	body := `{"id":"chatcmpl-1","object":"chat.completion","model":"meta/llama3-405b-instruct-maas","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":6,"completion_tokens":3,"total_tokens":9}}`
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: prov_ali_repl_vertex_nopCloser(body)}
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: 1, UpstreamModelName: "meta/llama3-405b-instruct-maas"},
	}
	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage.(*dto.Usage).PromptTokens != 6 || usage.(*dto.Usage).CompletionTokens != 3 || usage.(*dto.Usage).TotalTokens != 9 {
		t.Errorf("usage = %+v, want PromptTokens=6 CompletionTokens=3 TotalTokens=9", usage)
	}
	if !strings.Contains(w.Body.String(), `"hi"`) {
		t.Errorf("body = %q, want the upstream content forwarded", w.Body.String())
	}
}
