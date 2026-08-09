package mistral

// Business-acceptance tests for the remaining Mistral adaptor gaps not
// covered by the existing cov_prov-longtail suite: DoRequest's transport
// delegation, DoResponse's streaming dispatch branch, and the
// requestOpenAI2Mistral case where a non-conforming tool_call_id shows up on
// a "tool" message with no prior tool_calls rewrite establishing the id
// mapping in this request (e.g. the assistant's tool_calls were sent in an
// earlier turn, so only the id on this message is available to rewrite).

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/app"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"

	"github.com/gin-gonic/gin"
)

func init() {
	// OaiStreamHandler's scanner builds a time.NewTicker(StreamingTimeout*time.Second);
	// a zero/unset value panics ("non-positive interval").
	if constant.StreamingTimeout <= 0 {
		constant.StreamingTimeout = 60
	}
}

func newR5provMistralCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c, w
}

// ---------------------------------------------------------------------------
// DoRequest: delegates to provider.DoApiRequest, running GetRequestURL +
// SetupRequestHeader (Bearer auth) against the channel base URL.
// ---------------------------------------------------------------------------

func TestR5Mistral_DoRequest_DelegatesToApiRequest(t *testing.T) {
	app.InitHttpClient()
	// Loopback destinations are blocked by the relay SSRF dial guard by
	// design; allow private IPs for this test only so it exercises the
	// delegation itself, not the (separately tested) dial guard.
	fs := system_setting.GetFetchSetting()
	prevAllow := fs.AllowPrivateIp
	fs.AllowPrivateIp = true
	defer func() { system_setting.GetFetchSetting().AllowPrivateIp = prevAllow }()

	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	a := &Adaptor{}
	c, _ := newR5provMistralCtx()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		ChannelMeta:    &relaycommon.ChannelMeta{ChannelBaseUrl: srv.URL, ApiKey: "mis-key"},
		RequestURLPath: "/v1/chat/completions",
		RelayMode:      relayconstant.RelayModeChatCompletions,
	}
	result, err := a.DoRequest(c, info, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp, ok := result.(*http.Response)
	if !ok {
		t.Fatalf("result type = %T, want *http.Response", result)
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if gotAuth != "Bearer mis-key" {
		t.Errorf("upstream saw Authorization = %q, want Bearer mis-key", gotAuth)
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("upstream saw path = %q, want /v1/chat/completions", gotPath)
	}
}

// ---------------------------------------------------------------------------
// DoResponse: streaming branch delegates to openai.OaiStreamHandler (the
// non-stream branch is already covered by the longtail suite).
// ---------------------------------------------------------------------------

func TestR5Mistral_DoResponse_Stream_DelegatesToOaiStreamHandler(t *testing.T) {
	a := &Adaptor{}
	c, w := newR5provMistralCtx()
	info := &relaycommon.RelayInfo{
		IsStream:    true,
		RelayMode:   relayconstant.RelayModeChatCompletions,
		RelayFormat: "openai",
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "mistral-large-latest"},
	}
	sse := `data: {"id":"c1","model":"mistral-large-latest","choices":[{"delta":{"content":"hi"}}]}` + "\n\n" + "data: [DONE]\n\n"
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(sse)), Header: make(http.Header)}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	u, ok := usage.(*dto.Usage)
	if !ok {
		t.Fatalf("expected *dto.Usage, got %T", usage)
	}
	if u.CompletionTokens <= 0 {
		t.Errorf("CompletionTokens = %d, want >0 for a non-empty streamed reply", u.CompletionTokens)
	}
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// ---------------------------------------------------------------------------
// requestOpenAI2Mistral: a "tool" message carries a non-conforming
// tool_call_id with NO prior tool_calls entry in this request establishing
// the id mapping (idMap is empty when we reach this message). The id must
// still be rewritten to a fresh 9-char alphanumeric id (not left as-is,
// which Mistral would reject).
// ---------------------------------------------------------------------------

func TestR5Mistral_RequestConversion_ToolCallId_NonConforming_NoPriorMapping_StillRewritten(t *testing.T) {
	msg := dto.Message{
		Role:       "tool",
		Content:    "sunny",
		ToolCallId: "orphan_call_id_no_prior_mapping",
	}
	req := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{msg}}

	got := requestOpenAI2Mistral(req)
	if len(got.Messages) != 1 {
		t.Fatalf("Messages len = %d, want 1", len(got.Messages))
	}
	newID := got.Messages[0].ToolCallId
	if newID == "orphan_call_id_no_prior_mapping" {
		t.Fatal("non-conforming tool_call_id with no prior mapping must still be rewritten")
	}
	if !mistralToolCallIdRegexp.MatchString(newID) {
		t.Errorf("rewritten id %q does not match Mistral's required ^[a-zA-Z0-9]{9}$ pattern", newID)
	}
}

// Same original non-conforming id appearing twice as a lone ToolCallId (no
// tool_calls rewrite ever populates the map) must map to the SAME new id
// both times -- proves the idMap[message.ToolCallId] hit-branch (not just
// the miss-branch) is exercised for the ToolCallId path specifically.
func TestR5Mistral_RequestConversion_ToolCallId_NonConforming_RepeatedAcrossMessages_SameRewrite(t *testing.T) {
	msg1 := dto.Message{Role: "tool", Content: "a", ToolCallId: "shared_bad_id_needs_rewrite"}
	msg2 := dto.Message{Role: "tool", Content: "b", ToolCallId: "shared_bad_id_needs_rewrite"}
	req := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{msg1, msg2}}

	got := requestOpenAI2Mistral(req)
	if len(got.Messages) != 2 {
		t.Fatalf("Messages len = %d, want 2", len(got.Messages))
	}
	id1 := got.Messages[0].ToolCallId
	id2 := got.Messages[1].ToolCallId
	if id1 == "shared_bad_id_needs_rewrite" {
		t.Fatal("first occurrence must be rewritten")
	}
	if id1 != id2 {
		t.Errorf("id2 = %q, want same rewritten id as id1 (%q) for the same original id", id2, id1)
	}
}
