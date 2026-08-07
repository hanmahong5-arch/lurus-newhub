package handler

// Business-acceptance coverage for the near-zero-coverage dispatchers in
// relay.go (relayHandler / geminiRelayHandler / RelayMidjourney / RelayTask /
// taskRelayHandler / getChannel) and relay_count_tokens.go
// (RelayClaudeCountTokens's happy call-through path). All helper/type names
// carry an h5b_ prefix to avoid collisions with the other cov_*_test.go
// files being written concurrently in this package.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// ─── shared ctx/db helpers ─────────────────────────────────────────────────

// h5b_allowRequestBody lifts constant.MaxRequestBodyMB (which defaults to the
// zero value in a hermetic test process, causing GetRequestBody to reject
// every non-empty body as "too large") for the duration of one test. Mirrors
// the save/restore pattern already used across this repo's cov_*_test.go
// files wherever a handler needs to actually read a JSON body.
func h5b_allowRequestBody(t *testing.T) {
	t.Helper()
	prev := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 10
	t.Cleanup(func() { constant.MaxRequestBodyMB = prev })
}

func h5b_newCtx(method, path, body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	c.Request = httptest.NewRequest(method, path, reader)
	c.Request.Header.Set("Content-Type", "application/json")
	return c, w
}

var h5b_dbCounter atomic.Int64

// h5b_setupChannelSelectDB opens a fresh hermetic sqlite DB with the tables
// getChannel's ChannelMeta!=nil branch touches (channels + abilities), swaps
// it into repo.DB/repo.LOG_DB, and returns a cleanup func. Mirrors the
// pattern already used elsewhere in this package (handlerRelaySetupDB) but
// additionally migrates Ability, which that helper does not.
func h5b_setupChannelSelectDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:h5brelay%d?mode=memory&cache=shared", h5b_dbCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &repo.Log{}, &repo.Channel{}, &repo.Ability{}} {
		if migErr := db.AutoMigrate(tbl); migErr != nil {
			t.Fatalf("auto migrate %T: %v", tbl, migErr)
		}
	}

	prevDB := repo.DB
	prevLogDB := repo.LOG_DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevMemCache := common.MemoryCacheEnabled

	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.MemoryCacheEnabled = false // force the direct-DB lookup path in CacheGetRandomSatisfiedChannel

	cleanup := func() {
		repo.DB = prevDB
		repo.LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.MemoryCacheEnabled = prevMemCache
		if sqlDB, sqlErr := db.DB(); sqlErr == nil {
			_ = sqlDB.Close()
		}
	}
	return db, cleanup
}

// ─── relayHandler ───────────────────────────────────────────────────────────

// TestH5b_RelayHandler_DefaultDispatchesToTextHelper pins the default-branch
// dispatch (any RelayMode not explicitly listed, e.g. chat completions) to
// relay.TextHelper. A relay carrying a Request of the wrong Go type for that
// path (as would happen if a future bug wired a Claude/Gemini request into
// the OpenAI-format branch) must fail fast and loud rather than silently
// treating it as an empty request.
func TestH5b_RelayHandler_DefaultDispatchesToTextHelper(t *testing.T) {
	c, _ := h5b_newCtx(http.MethodPost, "/v1/chat/completions", "")
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions}

	apiErr := relayHandler(c, info)

	if apiErr == nil {
		t.Fatal("expected an error (nil Request cannot satisfy TextHelper's type assertion)")
	}
	if !strings.Contains(apiErr.Error(), "invalid request type") {
		t.Errorf("error = %q, want it to name the invalid-request-type failure from TextHelper", apiErr.Error())
	}
}

// TestH5b_RelayHandler_ImagesDispatchesToImageHelper pins the
// RelayModeImagesGenerations branch to relay.ImageHelper specifically (as
// opposed to falling through to the default TextHelper branch, which would
// silently mis-route every image request).
func TestH5b_RelayHandler_ImagesDispatchesToImageHelper(t *testing.T) {
	c, _ := h5b_newCtx(http.MethodPost, "/v1/images/generations", "")
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesGenerations}

	apiErr := relayHandler(c, info)

	if apiErr == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(apiErr.Error(), "ImageRequest") {
		t.Errorf("error = %q, want it to reference ImageRequest (proves ImageHelper, not TextHelper, ran)", apiErr.Error())
	}
}

// ─── geminiRelayHandler ─────────────────────────────────────────────────────

// TestH5b_GeminiRelayHandler_NonEmbedPath_DispatchesToGeminiHelper pins the
// path-based dispatch: a URL that does not contain "embed" must route to
// relay.GeminiHelper (chat), not the embedding handler.
func TestH5b_GeminiRelayHandler_NonEmbedPath_DispatchesToGeminiHelper(t *testing.T) {
	c, _ := h5b_newCtx(http.MethodPost, "/v1beta/models/gemini-pro:generateContent", "")
	info := &relaycommon.RelayInfo{}

	apiErr := geminiRelayHandler(c, info)

	if apiErr == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(apiErr.Error(), "GeminiChatRequest") {
		t.Errorf("error = %q, want it to reference GeminiChatRequest (proves GeminiHelper ran)", apiErr.Error())
	}
}

// TestH5b_GeminiRelayHandler_EmbedPath_DispatchesToEmbeddingHandler pins the
// complementary branch: a URL containing "embed" must route to
// relay.GeminiEmbeddingHandler rather than relay.GeminiHelper. Unlike the
// chat path (which type-asserts info.Request and fails immediately),
// GeminiEmbeddingHandler parses its own request straight from the body, so a
// malformed body surfaces as a JSON-decode NewAPIError from that parsing
// step — proof this request actually reached the embedding-specific code,
// since GeminiHelper's failure mode (an "invalid request type ... expected
// *dto.GeminiChatRequest" message) never mentions JSON at all.
func TestH5b_GeminiRelayHandler_EmbedPath_DispatchesToEmbeddingHandler(t *testing.T) {
	h5b_allowRequestBody(t)
	c, _ := h5b_newCtx(http.MethodPost, "/v1beta/models/embedding-001:embedContent", `{"content":`) // truncated
	info := &relaycommon.RelayInfo{}

	apiErr := geminiRelayHandler(c, info)

	if apiErr == nil {
		t.Fatal("expected an error for a truncated JSON body")
	}
	if strings.Contains(apiErr.Error(), "GeminiChatRequest") {
		t.Errorf("error = %q, want the embedding-specific JSON-decode failure, not GeminiHelper's request-type message", apiErr.Error())
	}
}

// ─── RelayMidjourney ────────────────────────────────────────────────────────

// TestH5b_RelayMidjourney_ImagineMissingPrompt_Returns400 exercises the
// default-branch dispatch (RelayMidjourneySubmit) end to end through the
// public handler: a real /mj/submit/imagine request missing the required
// prompt field must come back as a 400 with the Midjourney-shaped error
// envelope, not a 500 or a silently-accepted empty task.
func TestH5b_RelayMidjourney_ImagineMissingPrompt_Returns400(t *testing.T) {
	h5b_allowRequestBody(t)
	c, w := h5b_newCtx(http.MethodPost, "/mj/submit/imagine", "{}")

	RelayMidjourney(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	if int(resp["code"].(float64)) != 4 { // constant.MjRequestError
		t.Errorf("code = %v, want 4 (MjRequestError)", resp["code"])
	}
	if !strings.Contains(fmt.Sprint(resp["description"]), "prompt_is_required") {
		t.Errorf("description = %v, want it to mention prompt_is_required", resp["description"])
	}
}

// TestH5b_RelayMidjourney_MalformedBody_ReturnsBindError exercises the
// request-body-parsing failure branch inside RelayMidjourneySubmit, reached
// before relayInfo.RelayMode is even consulted.
func TestH5b_RelayMidjourney_MalformedBody_ReturnsBindError(t *testing.T) {
	h5b_allowRequestBody(t)
	c, w := h5b_newCtx(http.MethodPost, "/mj/submit/imagine", `{"prompt":`) // truncated

	RelayMidjourney(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	if !strings.Contains(fmt.Sprint(resp["description"]), "bind_request_body_failed") {
		t.Errorf("description = %v, want bind_request_body_failed", resp["description"])
	}
}

// ─── taskRelayHandler ───────────────────────────────────────────────────────

// TestH5b_TaskRelayHandler_FetchModeSucceeds pins the fetch-family dispatch
// (RelayModeSunoFetch et al -> relay.RelayTaskFetch): with an empty "ids"
// list, the real business behaviour is an empty-but-successful task list,
// not an error — proving the handler routed to the fetch path (which writes
// straight to c.Writer) and not the submit path (which would fail on the
// unset platform below).
func TestH5b_TaskRelayHandler_FetchModeSucceeds(t *testing.T) {
	h5b_allowRequestBody(t)
	c, w := h5b_newCtx(http.MethodPost, "/suno/fetch", "{}")
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeSunoFetch}

	taskErr := taskRelayHandler(c, info)

	if taskErr != nil {
		t.Fatalf("unexpected taskErr: %+v", taskErr)
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (default status on the first Write to an untouched ResponseWriter)", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"code":"success"`) {
		t.Errorf("body = %s, want the success envelope from RelayTaskFetch", w.Body.String())
	}
}

// TestH5b_TaskRelayHandler_DefaultDispatchesToSubmit pins the default-branch
// dispatch to relay.RelayTaskSubmit: with no channel_type/platform
// configured on the context, submission must fail closed with a named
// "invalid_api_platform" error rather than silently picking an arbitrary
// vendor adaptor.
func TestH5b_TaskRelayHandler_DefaultDispatchesToSubmit(t *testing.T) {
	c, _ := h5b_newCtx(http.MethodPost, "/v1/videos", "{}")
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeUnknown, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}

	taskErr := taskRelayHandler(c, info)

	if taskErr == nil {
		t.Fatal("expected a taskErr")
	}
	if taskErr.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want 400", taskErr.StatusCode)
	}
	if !strings.Contains(fmt.Sprint(taskErr.Message), "invalid api platform") && taskErr.Code != "invalid_api_platform" {
		t.Errorf("taskErr = %+v, want an invalid_api_platform failure", taskErr)
	}
}

// ─── RelayTask (full handler) ───────────────────────────────────────────────

// TestH5b_RelayTask_SubmitFailsClosed_NoChannel_WritesJSONAndRecordsVideoFailure
// drives the exported entry point end to end: GenRelayInfo -> taskRelayHandler
// (submit path, fails on invalid_api_platform, StatusCode 400 so
// shouldRetryTaskRelay short-circuits the retry loop) -> the final
// c.JSON(taskErr.StatusCode, taskErr) write. The model name is chosen to
// contain "video" so this also exercises the isVideoModel/RecordVideoModelFailure
// tail, which only fires for the video-model-name business case.
func TestH5b_RelayTask_SubmitFailsClosed_NoChannel_WritesJSONAndRecordsVideoFailure(t *testing.T) {
	c, w := h5b_newCtx(http.MethodPost, "/v1/videos/generations", "{}")
	c.Set("original_model", "h5b-video-model-x")

	RelayTask(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid_api_platform") {
		t.Errorf("body = %s, want invalid_api_platform", w.Body.String())
	}
}

// ─── getChannel (ChannelMeta != nil branch) ────────────────────────────────

// TestH5b_GetChannel_ChannelMetaSet_NoChannelAvailable pins the retry-path
// branch of getChannel (ChannelMeta != nil): with an empty abilities table
// for the requested group/model, CacheGetRandomSatisfiedChannel legitimately
// returns (nil, nil, nil) — no channel found, not a DB error — and getChannel
// must turn that into a client-facing "no available channel" error rather
// than dereferencing a nil channel or returning success with channel==nil.
func TestH5b_GetChannel_ChannelMetaSet_NoChannelAvailable(t *testing.T) {
	_, cleanup := h5b_setupChannelSelectDB(t)
	defer cleanup()

	c, _ := h5b_newCtx(http.MethodPost, "/v1/chat/completions", "")
	info := &relaycommon.RelayInfo{
		ChannelMeta:     &relaycommon.ChannelMeta{},
		OriginModelName: "h5b-no-such-model",
	}
	retryParam := &app.RetryParam{
		Ctx:        c,
		TokenGroup: "h5b-empty-group",
		ModelName:  "h5b-no-such-model",
		Retry:      common.GetPointer(0),
	}

	ch, apiErr := getChannel(c, info, retryParam)

	if ch != nil {
		t.Errorf("channel = %+v, want nil when no channel is registered for the group/model", ch)
	}
	if apiErr == nil {
		t.Fatal("expected an error when no channel satisfies the group/model")
	}
	if !types.IsSkipRetryError(apiErr) {
		t.Errorf("apiErr should carry ErrOptionWithSkipRetry so the outer retry loop breaks immediately, got: %+v", apiErr)
	}
}

// ─── RelayClaudeCountTokens (happy call-through path) ──────────────────────

// TestH5b_RelayClaudeCountTokens_NonAnthropicChannel_Returns501 exercises the
// line that the existing validation-only tests in this package never reach:
// a well-formed request that clears GetAndValidateRequest and GenRelayInfo
// and actually calls relay.ClaudeCountTokensHelper. With the context's
// channel_type set to a non-Anthropic vendor (OpenAI), the helper must refuse
// with a 501 naming the mismatch rather than forwarding the count_tokens call
// to an upstream that doesn't implement Anthropic's endpoint.
func TestH5b_RelayClaudeCountTokens_NonAnthropicChannel_Returns501(t *testing.T) {
	c, w := h5b_newCtx(http.MethodPost, "/v1/messages/count_tokens",
		`{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":"hi"}]}`)
	c.Set("channel_type", 1) // constant.ChannelTypeOpenAI

	RelayClaudeCountTokens(c)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501, body: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	if resp["type"] != "error" {
		t.Errorf(`resp["type"] = %v, want "error"`, resp["type"])
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("resp[error] missing: %v", resp)
	}
	if !strings.Contains(fmt.Sprint(errObj["message"]), "count_tokens") {
		t.Errorf("error.message = %v, want it to name the count_tokens endpoint mismatch", errObj["message"])
	}
}
