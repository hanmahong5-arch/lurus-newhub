package minimax

// Business-acceptance tests filling the remaining gaps in the minimax
// adaptor: DoRequest's end-to-end HTTP round trip (0% covered by the
// existing longtail suite), DoResponse's audio-speech dispatch branch
// (previously only exercised by calling handleTTSResponse directly, not
// through the public DoResponse entry point), the format->content-type
// lookup table used by TTS responses, and the "upstream body unreadable"
// branches of handleTTSResponse/handleChatCompletionResponse.

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"

	"github.com/gin-gonic/gin"
)

func provLt1MinimaxAllowPrivateIP(t *testing.T) {
	t.Helper()
	fs := system_setting.GetFetchSetting()
	prev := fs.AllowPrivateIp
	fs.AllowPrivateIp = true
	t.Cleanup(func() { system_setting.GetFetchSetting().AllowPrivateIp = prev })
}

func provLt1MinimaxClosedPortURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return "http://" + addr
}

// provLt1MinimaxErrorBody always fails on Read, modelling a connection
// dropped mid-response.
type provLt1MinimaxErrorBody struct{}

func (provLt1MinimaxErrorBody) Read([]byte) (int, error) { return 0, errors.New("connection reset") }
func (provLt1MinimaxErrorBody) Close() error              { return nil }

func provLt1MinimaxCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", nil)
	return c, w
}

// ---------------------------------------------------------------------------
// DoRequest: delegates to provider.DoApiRequest(a, c, info, requestBody).
// ---------------------------------------------------------------------------

func TestMinimaxLt1_DoRequest_Success(t *testing.T) {
	app.InitHttpClient()
	provLt1MinimaxAllowPrivateIP(t)

	var gotAuth, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"base_resp":{"status_code":0}}`))
	}))
	defer server.Close()

	a := &Adaptor{}
	c, _ := provLt1MinimaxCtx()
	info := &relaycommon.RelayInfo{
		RelayMode:   constant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: server.URL, ApiKey: "mm-secret"},
	}

	resp, err := a.DoRequest(c, info, strings.NewReader(`{"model":"abab6.5"}`))
	if err != nil {
		t.Fatalf("DoRequest() error = %v", err)
	}
	httpResp, ok := resp.(*http.Response)
	if !ok {
		t.Fatalf("expected *http.Response, got %T", resp)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", httpResp.StatusCode)
	}
	if gotAuth != "Bearer mm-secret" {
		t.Errorf("upstream Authorization = %q, want %q", gotAuth, "Bearer mm-secret")
	}
	if gotBody != `{"model":"abab6.5"}` {
		t.Errorf("upstream body = %q, want request body forwarded verbatim", gotBody)
	}
}

func TestMinimaxLt1_DoRequest_NetworkErrorWrapsDoRequestFailed(t *testing.T) {
	app.InitHttpClient()
	provLt1MinimaxAllowPrivateIP(t)

	a := &Adaptor{}
	c, _ := provLt1MinimaxCtx()
	info := &relaycommon.RelayInfo{
		RelayMode:   constant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: provLt1MinimaxClosedPortURL(t), ApiKey: "k"},
	}

	_, err := a.DoRequest(c, info, strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), "do request failed") {
		t.Fatalf("err = %v, want wrapped 'do request failed'", err)
	}
}

// ---------------------------------------------------------------------------
// DoResponse: audio-speech dispatch branch.
// ---------------------------------------------------------------------------

func TestMinimaxLt1_DoResponse_AudioSpeech_DispatchesToTTSHandler(t *testing.T) {
	a := &Adaptor{}
	c, w := provLt1MinimaxCtx()
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeAudioSpeech, ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"data":{"audio":"68656c6c6f","status":1},"extra_info":{"usage_characters":11},"base_resp":{"status_code":0}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}

	usage, apiErr := a.DoResponse(c, resp, info)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	if usage == nil {
		t.Fatal("expected non-nil usage envelope from the TTS handler dispatch")
	}
	if w.Body.String() != "hello" {
		t.Errorf("response body = %q, want decoded hex audio %q (DoResponse must reach handleTTSResponse, not the chat passthrough)", w.Body.String(), "hello")
	}
}

// ---------------------------------------------------------------------------
// getContentTypeByFormat: format -> MIME type lookup table.
// ---------------------------------------------------------------------------

func TestMinimaxLt1_GetContentTypeByFormat(t *testing.T) {
	cases := []struct {
		format string
		want   string
	}{
		{"mp3", "audio/mpeg"},
		{"wav", "audio/wav"},
		{"flac", "audio/flac"},
		{"aac", "audio/aac"},
		{"pcm", "audio/pcm"},
		{"unknown-format", "audio/mpeg"},
		{"", "audio/mpeg"},
	}
	for _, tc := range cases {
		t.Run(tc.format, func(t *testing.T) {
			got := getContentTypeByFormat(tc.format)
			if got != tc.want {
				t.Errorf("getContentTypeByFormat(%q) = %q, want %q", tc.format, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// handleTTSResponse / handleChatCompletionResponse: unreadable upstream body.
// ---------------------------------------------------------------------------

func TestMinimaxLt1_HandleTTSResponse_UnreadableBodyErrors(t *testing.T) {
	c, _ := provLt1MinimaxCtx()
	info := &relaycommon.RelayInfo{}
	resp := &http.Response{StatusCode: 200, Body: provLt1MinimaxErrorBody{}}

	usage, apiErr := handleTTSResponse(c, resp, info)
	if apiErr == nil {
		t.Fatal("expected error when the upstream TTS body cannot be read")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil on read failure", usage)
	}
}

func TestMinimaxLt1_HandleChatCompletionResponse_UnreadableBodyErrors(t *testing.T) {
	c, _ := provLt1MinimaxCtx()
	info := &relaycommon.RelayInfo{}
	resp := &http.Response{StatusCode: 200, Body: provLt1MinimaxErrorBody{}}

	usage, apiErr := handleChatCompletionResponse(c, resp, info)
	if apiErr == nil {
		t.Fatal("expected error when the upstream chat body cannot be read")
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil on read failure", usage)
	}
}
