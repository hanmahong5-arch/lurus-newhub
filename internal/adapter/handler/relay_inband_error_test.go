package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// Relay's deferred error renderer, once SSE bytes have been flushed, must emit
// the caller's wire-native in-band error (helper.StreamError). Before
// 2026-09-02 the Claude wire got a data-only {"type":"error"} frame — no
// `event:` line, which the Anthropic SDK dispatches on, so the error was
// silently dropped — and the Gemini wire got an OpenAI envelope plus a [DONE]
// line the google-genai SDK cannot json-decode.
//
// The error is provoked at the first gate (empty body fails request
// validation) on a writer that already carries a flushed frame.
func TestRelay_InBandErrorAfterStreamStarted_IsWireNative(t *testing.T) {
	prev := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 10
	t.Cleanup(func() { constant.MaxRequestBodyMB = prev })

	cases := []struct {
		format  types.RelayFormat
		path    string
		want    []string
		mustNot []string
	}{
		{
			types.RelayFormatClaude, "/v1/messages",
			[]string{"event: error\n", `data: {"type":"error","error":{`},
			[]string{"[DONE]"},
		},
		{
			types.RelayFormatGemini, "/v1beta/models/m:streamGenerateContent",
			[]string{`data: {"error":{"code":400,"message":`, `"status":"INVALID_ARGUMENT"`},
			[]string{"[DONE]"},
		},
		{
			types.RelayFormatOpenAI, "/v1/chat/completions",
			[]string{`data: {"error":{"message":`, "data: [DONE]"},
			[]string{"event: error"},
		},
	}
	for _, tc := range cases {
		t.Run(string(tc.format), func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(""))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Writer.Header().Set("Content-Type", "text/event-stream")
			if _, err := c.Writer.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n"); err != nil {
				t.Fatal(err)
			}
			c.Writer.Flush()

			Relay(c, tc.format)

			body := w.Body.String()
			tail := body[strings.Index(body, "\n\n")+2:]
			for _, want := range tc.want {
				if !strings.Contains(tail, want) {
					t.Errorf("%s: in-band error missing %q:\n%s", tc.format, want, body)
				}
			}
			for _, bad := range tc.mustNot {
				if strings.Contains(tail, bad) {
					t.Errorf("%s: in-band error must not contain %q:\n%s", tc.format, bad, body)
				}
			}
			if w.Code != http.StatusOK {
				t.Errorf("status already sent; got %d (a late status would corrupt the stream)", w.Code)
			}
		})
	}
}

// TestRelay_NonStreamErrorEnvelope_IsWireNative is the not-yet-written half of
// the test above. When nothing has been flushed the deferred renderer takes the
// c.JSON path, which had a Claude case and an OpenAI default and no Gemini case
// at all — so a google-genai caller received an OpenAI envelope, failed to
// recognise its own error shape, and surfaced a parse failure instead of the
// reason its request was rejected.
func TestRelay_NonStreamErrorEnvelope_IsWireNative(t *testing.T) {
	prev := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 10
	t.Cleanup(func() { constant.MaxRequestBodyMB = prev })

	cases := []struct {
		format  types.RelayFormat
		path    string
		want    []string
		mustNot []string
	}{
		{
			// Gemini's own envelope: {"error":{code,message,status}}, with
			// google.rpc status names — not {"error":{"message","type"}}.
			types.RelayFormatGemini, "/v1beta/models/m:generateContent",
			[]string{`{"error":{"code":400,`, `"status":"INVALID_ARGUMENT"`},
			[]string{`"type":"new_api_error"`},
		},
		{
			types.RelayFormatClaude, "/v1/messages",
			[]string{`"type":"error"`, `"error":{`},
			[]string{`"status":"INVALID_ARGUMENT"`},
		},
		{
			types.RelayFormatOpenAI, "/v1/chat/completions",
			[]string{`{"error":{`, `"message":`},
			[]string{`"status":"INVALID_ARGUMENT"`},
		},
	}
	for _, tc := range cases {
		t.Run(string(tc.format), func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(""))
			c.Request.Header.Set("Content-Type", "application/json")

			Relay(c, tc.format)

			body := w.Body.String()
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("%s: error body missing %q:\n%s", tc.format, want, body)
				}
			}
			for _, bad := range tc.mustNot {
				if strings.Contains(body, bad) {
					t.Errorf("%s: error body must not contain %q:\n%s", tc.format, bad, body)
				}
			}
			// Nothing was streamed, so the status line is still ours to set and
			// must carry the real status rather than 200.
			if w.Code != http.StatusBadRequest {
				t.Errorf("%s: status = %d, want 400", tc.format, w.Code)
			}
		})
	}
}
