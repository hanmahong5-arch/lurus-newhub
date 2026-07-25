package relay

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"

	"github.com/gin-gonic/gin"
)

// countTokensFixture wires a gin context against a stub upstream, mirroring the
// context keys middleware.Distribute sets before the handler runs.
func countTokensFixture(t *testing.T, channelType int, upstream *httptest.Server) (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo) {
	t.Helper()

	// main() does this at boot; provider.DoRequest dereferences the shared client.
	app.InitHttpClient()

	// The stub upstream is on loopback, which the relay SSRF dial guard blocks by
	// design. Allow private destinations for the duration of this test only.
	fs := system_setting.GetFetchSetting()
	prevAllow := fs.AllowPrivateIp
	fs.AllowPrivateIp = true
	t.Cleanup(func() {
		system_setting.GetFetchSetting().AllowPrivateIp = prevAllow
	})

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens",
		strings.NewReader(`{"model":"claude-3","messages":[{"role":"user","content":"hi"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")

	common.SetContextKey(c, constant.ContextKeyChannelType, channelType)
	common.SetContextKey(c, constant.ContextKeyChannelId, 1)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "sk-upstream-test")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "claude-3")
	if upstream != nil {
		common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
	}

	info := &relaycommon.RelayInfo{
		Request:         &dto.ClaudeRequest{Model: "claude-3", MaxTokens: 16},
		OriginModelName: "claude-3",
	}
	return c, w, info
}

func TestClaudeCountTokensHelper_ProxiesUpstreamVerbatim(t *testing.T) {
	var gotPath, gotAPIKey, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("x-api-key")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"input_tokens":2095}`))
	}))
	defer upstream.Close()

	c, w, info := countTokensFixture(t, constant.ChannelTypeAnthropic, upstream)

	if apiErr := ClaudeCountTokensHelper(c, info); apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}

	if gotPath != "/v1/messages/count_tokens" {
		t.Errorf("upstream path = %q, want /v1/messages/count_tokens", gotPath)
	}
	if gotAPIKey != "sk-upstream-test" {
		t.Errorf("channel key not forwarded, x-api-key = %q", gotAPIKey)
	}
	if !strings.Contains(gotBody, `"model":"claude-3"`) {
		t.Errorf("request body not forwarded: %s", gotBody)
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var decoded struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("response is not the upstream JSON: %v (%s)", err, w.Body.String())
	}
	if decoded.InputTokens != 2095 {
		t.Errorf("input_tokens = %d, want the upstream's 2095 (no local re-estimation)", decoded.InputTokens)
	}
}

// The caller must see the upstream's own rejection, not a gateway-invented one:
// SDKs branch on these codes.
func TestClaudeCountTokensHelper_PassesUpstreamErrorThrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"bad model"}}`))
	}))
	defer upstream.Close()

	c, w, info := countTokensFixture(t, constant.ChannelTypeAnthropic, upstream)

	if apiErr := ClaudeCountTokensHelper(c, info); apiErr != nil {
		t.Fatalf("upstream 4xx must be proxied, not converted to a gateway error: %v", apiErr)
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want the upstream's 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "invalid_request_error") {
		t.Errorf("upstream error body lost: %s", w.Body.String())
	}
}

// A channel that does not speak the Anthropic protocol has no such endpoint;
// say so instead of forwarding and surfacing an opaque upstream 404.
func TestClaudeCountTokensHelper_RejectsNonAnthropicChannel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("non-Anthropic channel must not be dialled at all")
	}))
	defer upstream.Close()

	c, _, info := countTokensFixture(t, constant.ChannelTypeOpenAI, upstream)

	apiErr := ClaudeCountTokensHelper(c, info)
	if apiErr == nil {
		t.Fatal("expected a rejection for a non-Anthropic channel")
	}
	if apiErr.StatusCode != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", apiErr.StatusCode)
	}
}

func TestClaudeCountTokensHelper_RejectsWrongRequestType(t *testing.T) {
	c, _, info := countTokensFixture(t, constant.ChannelTypeAnthropic, nil)
	info.Request = &dto.GeneralOpenAIRequest{Model: "claude-3"}

	apiErr := ClaudeCountTokensHelper(c, info)
	if apiErr == nil {
		t.Fatal("expected a rejection for a non-Claude request body")
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", apiErr.StatusCode)
	}
}
