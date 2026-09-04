package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// A prompt rejected by our own blocklist is a client error we are certain
// about, and it used to be reported as a 500.
//
// The call site passed types.NewError(err, ErrorCodeSensitiveWordsDetected)
// with an `err` that is provably nil at that point (both preceding assignments
// return on failure). NewAPIError.Error() therefore fell back to the bare
// error-code string, and NewError's default 500 made IsUpstreamFailure true —
// so the caller was told "upstream provider Unknown returned 500" about a
// prompt we deliberately refused, and our own policy decision was counted
// toward the upstream 5xx rate.
func TestRelay_SensitivePromptRejection_Is400WithReadableMessage(t *testing.T) {
	prevMB := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 10
	prevWords := setting.SensitiveWords
	prevEnabled := setting.CheckSensitiveEnabled
	prevPrompt := setting.CheckSensitiveOnPromptEnabled
	setting.SensitiveWords = []string{"test_sensitive"}
	setting.CheckSensitiveEnabled = true
	setting.CheckSensitiveOnPromptEnabled = true
	t.Cleanup(func() {
		constant.MaxRequestBodyMB = prevMB
		setting.SensitiveWords = prevWords
		setting.CheckSensitiveEnabled = prevEnabled
		setting.CheckSensitiveOnPromptEnabled = prevPrompt
	})

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"test_sensitive please"}]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	Relay(c, types.RelayFormatOpenAI)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400. We rejected the request on purpose and know why; "+
			"a 5xx blames a vendor for our policy and inflates the upstream error rate.", w.Code)
	}

	got := w.Body.String()
	// The old body was the bare code string as the message.
	if strings.Contains(got, `"message":"sensitive_words_detected`) {
		t.Errorf("message is the raw error code — nothing a caller can act on:\n%s", got)
	}
	if !strings.Contains(got, "blocked term") {
		t.Errorf("message does not explain the rejection:\n%s", got)
	}
	// The machine-readable code stays where clients already look for it.
	if !strings.Contains(got, `"code":"sensitive_words_detected"`) {
		t.Errorf("error code must remain sensitive_words_detected:\n%s", got)
	}

	// The matched terms must never be echoed: doing so turns this endpoint into
	// an oracle for enumerating the blocklist one probe at a time.
	if strings.Contains(got, "test_sensitive") {
		t.Errorf("response echoed the matched term back to the caller — that is a "+
			"blocklist-enumeration oracle:\n%s", got)
	}
}

// The Gemini wire must get its own envelope for this rejection too, not an
// OpenAI one — the same not-yet-written path fixed alongside it.
func TestRelay_SensitivePromptRejection_GeminiEnvelope(t *testing.T) {
	prevMB := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 10
	prevWords := setting.SensitiveWords
	setting.SensitiveWords = []string{"test_sensitive"}
	t.Cleanup(func() {
		constant.MaxRequestBodyMB = prevMB
		setting.SensitiveWords = prevWords
	})

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"contents":[{"role":"user","parts":[{"text":"test_sensitive please"}]}]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.0-flash:generateContent", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	Relay(c, types.RelayFormatGemini)

	got := w.Body.String()
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400:\n%s", w.Code, got)
	}
	if !strings.Contains(got, `{"error":{"code":400,`) || !strings.Contains(got, `"status":"INVALID_ARGUMENT"`) {
		t.Errorf("Gemini caller did not get Gemini's error envelope:\n%s", got)
	}
	if strings.Contains(got, "test_sensitive") {
		t.Errorf("response echoed the matched term:\n%s", got)
	}
}
