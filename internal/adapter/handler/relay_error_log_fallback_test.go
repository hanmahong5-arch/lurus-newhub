package handler

// Pins the terminal-error fallback in Relay's deferred renderer: errors that
// die BEFORE any channel attempt (request binding/validation, estimation,
// pricing) used to leave no error-log row — processChannelError only runs
// after a channel was tried. Live-probed 2026-08-30: a max_tokens:-5 binding
// error returned 400 with the logs table untouched.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
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
	c.Request.Header.Set(ratio_setting.SourceProductHeader, "lutu")
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
	// Attribution is resolved off the raw header here because this failure
	// happens before GenRelayInfo ever runs (relay.go recordRelayErrorLog).
	if !strings.Contains(lg.Other, `"source_product":"lutu"`) {
		t.Errorf("Other = %q, want source_product lutu on a pre-GenRelayInfo error row", lg.Other)
	}
}

// TestRelay_PreChannelBindingError_CopiesUpstreamRequestIdWhenSet drives the
// same pre-channel binding-error path as TestRelay_PreChannelBindingError_
// RecordsErrorLog, but pre-seeds "upstream_request_id" the way provider.
// doRequest would have left it from an EARLIER channel attempt in the same
// request's retry loop (recordRelayErrorLog reads it via c.GetString
// regardless of which stage set it — it does not require simulating a
// channel-stage error; this is the exact same function the sibling test
// above already drives). A 5xx
// from upstream on attempt 1 followed by a terminal pre-channel-style error
// is not how retries actually fail, but the read is on the shared
// gin.Context regardless of which stage set it, so this is a faithful lock
// on relay.go's copy at :780.
func TestRelay_PreChannelBindingError_CopiesUpstreamRequestIdWhenSet(t *testing.T) {
	db, cleanup := handlerRelaySetupDB(t)
	defer cleanup()
	errorLogFallbackEnable(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{not-json`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", 55)
	c.Set("token_id", 77)
	c.Set("tenant_id", "acme-corp")
	c.Set("original_model", "gpt-4o")
	c.Set("upstream_request_id", "vend-err-1")

	Relay(c, types.RelayFormatOpenAI)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for malformed body; body=%s", w.Code, w.Body.String())
	}
	var logs []repo.Log
	if err := db.Where("type = ?", repo.LogTypeError).Find(&logs).Error; err != nil {
		t.Fatalf("query error logs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("error log rows = %d, want 1", len(logs))
	}
	if !strings.Contains(logs[0].Other, `"upstream_request_id":"vend-err-1"`) {
		t.Errorf("Other = %q, want upstream_request_id vend-err-1 copied onto the error row", logs[0].Other)
	}
}

// TestRelay_PreChannelBindingError_OmitsUpstreamRequestIdWhenUnset is the
// mirror of the test above: when no attempt ever reached provider.doRequest,
// the error row must not carry the key at all (not even empty-string) —
// matching the "written only when present" rule the success path follows.
func TestRelay_PreChannelBindingError_OmitsUpstreamRequestIdWhenUnset(t *testing.T) {
	db, cleanup := handlerRelaySetupDB(t)
	defer cleanup()
	errorLogFallbackEnable(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{not-json`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", 55)
	c.Set("token_id", 77)
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
		t.Fatalf("error log rows = %d, want 1", len(logs))
	}
	if strings.Contains(logs[0].Other, "upstream_request_id") {
		t.Errorf("Other = %q, want no upstream_request_id key when no attempt ever reached doRequest", logs[0].Other)
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

// TestRelay_RetryAcrossChannels_DoesNotLeakPreviousAttemptsUpstreamRequestId
// is the round-2 residual lock (findings 8/11/15): provider.doRequest only
// clears the shared "upstream_request_id" key right before its own
// client.Do call, so an attempt that fails BEFORE ever reaching doRequest —
// header/param override validation, GetRequestURL, request conversion — must
// not let recordRelayErrorLog stamp its error row with the id an EARLIER
// attempt captured from a DIFFERENT channel. Two real attempts run through
// the retry loop via the getChannelFn seam (the same seam
// rate_limit_headroom_strip_test.go's TestRelay_AllKeysCooling_* uses):
// attempt 1 (channel A) reaches a real httptest upstream, gets a 500 with
// x-request-id, and is retried; attempt 2 (channel B) carries a header
// override with a non-string value, so provider.processHeaderOverride
// rejects it before doRequest is ever called. The fix under test is
// relay.go's per-iteration reset placed right after addUsedChannel.
func TestRelay_RetryAcrossChannels_DoesNotLeakPreviousAttemptsUpstreamRequestId(t *testing.T) {
	db, cleanup := handlerRelaySetupDB(t)
	defer cleanup()
	errorLogFallbackEnable(t)

	prevRetryTimes := common.RetryTimes
	common.RetryTimes = 1
	t.Cleanup(func() { common.RetryTimes = prevRetryTimes })

	// The relay egress client and SSRF dial guard are boot-time singletons
	// (cmd/server/main.go); a hermetic test process never runs boot, and the
	// guard defaults to blocking the httptest upstream's private 127.0.0.1
	// address. Same recipe as relay_success_fixture_test.go.
	app.InitHttpClient()
	fs := system_setting.GetFetchSetting()
	prevFetchSetting := *fs
	fs.AllowPrivateIp = true
	t.Cleanup(func() { *fs = prevFetchSetting })

	const model = "l3-retry-leak-test-model"
	ratioSnapshot := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateModelRatioByJSONString(ratioSnapshot) })
	if err := ratio_setting.UpdateModelRatioByJSONString(`{"` + model + `":0}`); err != nil {
		t.Fatalf("seed model ratio: %v", err)
	}
	qs := operation_setting.GetQuotaSetting()
	prevFreeConsume := qs.EnableFreeModelPreConsume
	qs.EnableFreeModelPreConsume = false
	t.Cleanup(func() { qs.EnableFreeModelPreConsume = prevFreeConsume })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-request-id", "vend-A-500")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream boom"}}`))
	}))
	t.Cleanup(srv.Close)

	prevGetChannel := getChannelFn
	t.Cleanup(func() { getChannelFn = prevGetChannel })
	getChannelFn = func(c *gin.Context, info *relaycommon.RelayInfo, retryParam *app.RetryParam) (*repo.Channel, *types.NewAPIError) {
		id := 601
		headerOverride := map[string]interface{}{}
		if retryParam.GetRetry() > 0 {
			id = 602
			// Non-string value: provider.processHeaderOverride rejects this
			// before client.Do ever runs (api_request.go).
			headerOverride = map[string]interface{}{"X-Bad-Override": 42}
		}
		common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
		common.SetContextKey(c, constant.ContextKeyChannelId, id)
		common.SetContextKey(c, constant.ContextKeyChannelName, fmt.Sprintf("fixture-%d", id))
		common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, srv.URL)
		common.SetContextKey(c, constant.ContextKeyChannelKey, "sk-test-key")
		common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]interface{}{})
		common.SetContextKey(c, constant.ContextKeyChannelHeaderOverride, headerOverride)
		common.SetContextKey(c, constant.ContextKeyChannelIsMultiKey, false)
		autoBanInt := 0
		return &repo.Channel{Id: id, Type: constant.ChannelTypeOpenAI, Name: fmt.Sprintf("fixture-%d", id), AutoBan: &autoBanInt}, nil
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"model":"` + model + `","messages":[{"role":"user","content":"hi"}]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", 55)
	c.Set("token_id", 77)
	c.Set("tenant_id", "acme-corp")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, model)

	Relay(c, types.RelayFormatOpenAI)

	var logs []repo.Log
	if err := db.Where("type = ?", repo.LogTypeError).Order("id asc").Find(&logs).Error; err != nil {
		t.Fatalf("query error logs: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("error log rows = %d, want 2 (one per attempt); logs=%+v", len(logs), logs)
	}
	if !strings.Contains(logs[0].Other, `"upstream_request_id":"vend-A-500"`) {
		t.Errorf("attempt 1 (channel A, failed inside doRequest) Other = %q, want upstream_request_id vend-A-500", logs[0].Other)
	}
	if strings.Contains(logs[1].Other, "upstream_request_id") {
		t.Errorf("attempt 2 (channel B, failed BEFORE doRequest on a bad header override) Other = %q, want no upstream_request_id key — channel A's vendor id must not leak onto channel B's row", logs[1].Other)
	}
}
