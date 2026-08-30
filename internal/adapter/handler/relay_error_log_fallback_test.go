package handler

// Pins the terminal-error fallback in Relay's deferred renderer: errors that
// die BEFORE any channel attempt (request binding/validation, estimation,
// pricing) used to leave no error-log row — processChannelError only runs
// after a channel was tried. Live-probed 2026-08-30: a max_tokens:-5 binding
// error returned 400 with the logs table untouched.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

func errorLogFallbackEnable(t *testing.T) {
	t.Helper()
	prev := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = true
	t.Cleanup(func() { constant.ErrorLogEnabled = prev })
	if constant.MaxRequestBodyMB <= 0 {
		prevMB := constant.MaxRequestBodyMB
		constant.MaxRequestBodyMB = 64
		t.Cleanup(func() { constant.MaxRequestBodyMB = prevMB })
	}
}

// TestRelay_PreChannelBindingError_RecordsErrorLog drives the full Relay entry
// with a malformed body: GetAndValidateRequest fails before any channel is
// selected, and the deferred renderer must still leave an error-log row for
// the authenticated caller.
func TestRelay_PreChannelBindingError_RecordsErrorLog(t *testing.T) {
	db, cleanup := handlerRelaySetupDB(t)
	defer cleanup()
	errorLogFallbackEnable(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{not-json`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", 55)
	c.Set("token_id", 77)
	c.Set("token_name", "probe-token")
	c.Set("group", "default")
	c.Set("tenant_id", "acme-corp")
	c.Set("original_model", "gpt-4o")

	Relay(c, types.RelayFormatOpenAI)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for malformed body; body=%s", w.Code, w.Body.String())
	}
	var logs []repo.Log
	if err := db.Where("type = ?", repo.LogTypeError).Find(&logs).Error; err != nil {
		t.Fatalf("query error logs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("error log rows = %d, want 1 for a pre-channel binding error", len(logs))
	}
	lg := logs[0]
	if lg.UserId != 55 || lg.TenantId != "acme-corp" || lg.ModelName != "gpt-4o" {
		t.Errorf("row = user %d tenant %q model %q, want 55/acme-corp/gpt-4o", lg.UserId, lg.TenantId, lg.ModelName)
	}
	if !strings.Contains(lg.Other, string(types.ErrorCodeInvalidRequest)) {
		t.Errorf("Other = %q, want error code %s", lg.Other, types.ErrorCodeInvalidRequest)
	}
}

// TestRecordTerminalRelayError_SkipsWhenChannelStageHandled proves the
// no-double-write contract: after processChannelError has owned an error
// (recording it), the deferred fallback must NOT add a second row for the
// same request.
func TestRecordTerminalRelayError_SkipsWhenChannelStageHandled(t *testing.T) {
	db, cleanup := handlerRelaySetupDB(t)
	defer cleanup()
	errorLogFallbackEnable(t)

	c, _ := newTestCtx()
	c.Set("id", 55)
	c.Set("channel_id", 9)
	c.Set("channel_type", 1)
	c.Set("tenant_id", "acme-corp")

	chErr := *types.NewChannelError(9, 1, "primary-openai", false, "sk-test-key", false)
	apiErr := types.NewErrorWithStatusCode(errors.New("upstream 500"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)

	processChannelError(c, chErr, apiErr)
	recordTerminalRelayError(c, apiErr) // what Relay's defer does with the same terminal error

	var count int64
	db.Model(&repo.Log{}).Where("type = ?", repo.LogTypeError).Count(&count)
	if count != 1 {
		t.Errorf("error log rows = %d, want exactly 1 (channel stage recorded, fallback must skip)", count)
	}
}

// TestRecordTerminalRelayError_RespectsNoRecordOptOut: errors that opted out
// via ErrOptionWithNoRecordErrorLog (e.g. quota-exhausted, which has its own
// durable audit trail) must stay out of the error log even on the fallback
// path — otherwise a caller retrying a doomed request floods the table.
func TestRecordTerminalRelayError_RespectsNoRecordOptOut(t *testing.T) {
	db, cleanup := handlerRelaySetupDB(t)
	defer cleanup()
	errorLogFallbackEnable(t)

	c, _ := newTestCtx()
	c.Set("id", 55)

	optOut := types.NewError(errors.New("quota exhausted"), types.ErrorCodeTokenQuotaExhausted,
		types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	recordTerminalRelayError(c, optOut)

	var count int64
	db.Model(&repo.Log{}).Where("type = ?", repo.LogTypeError).Count(&count)
	if count != 0 {
		t.Errorf("error log rows = %d, want 0 for an opted-out error", count)
	}
}
