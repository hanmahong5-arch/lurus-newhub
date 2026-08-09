package handler

// This file adds business-acceptance-level edge-case coverage for
// relay.go, relay_count_tokens.go, openrouter_pool.go, playground.go and
// v2_chat.go. Helper names are prefixed handlerRelay to avoid collisions
// with other cov_*_test.go files being written concurrently in this
// package.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// ─── shared hermetic DB setup ──────────────────────────────────────────────

var handlerRelayDBCounter atomic.Int64

// handlerRelaySetupDB opens a fresh in-memory sqlite DB, migrates the tables
// this file's tests need, swaps it into repo.DB/repo.LOG_DB, and returns a
// cleanup func that restores everything. Mirrors the pattern already used by
// playground_fanout_test.go / v2_chat_test.go / testutil_integration_test.go.
func handlerRelaySetupDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:handlerrelay%d?mode=memory&cache=shared", handlerRelayDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &repo.Log{}, &repo.Channel{}} {
		if migErr := db.AutoMigrate(tbl); migErr != nil && !strings.Contains(migErr.Error(), "already exists") {
			t.Fatalf("auto migrate %T: %v", tbl, migErr)
		}
	}

	prevDB := repo.DB
	prevLogDB := repo.LOG_DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled

	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	cleanup := func() {
		repo.DB = prevDB
		repo.LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		if sqlDB, sqlErr := db.DB(); sqlErr == nil {
			_ = sqlDB.Close()
		}
	}
	return db, cleanup
}

// ─── maskKeyPrefix ──────────────────────────────────────────────────────────

func TestHandlerRelay_MaskKeyPrefix_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{"empty", "", "***"},
		{"under_visible_len", "short", "short***"},
		{"exactly_12_chars", "sk-or-v1-AAA", "sk-or-v1-AAA***"}, // len==12 -> "<=visible" branch, whole key + ***
		{"over_12_chars", "sk-or-v1-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "sk-or-v1-AAA***"},
		{"unicode_multibyte", "密钥密钥密钥密钥密钥密钥密钥密钥", "密钥密钥密钥密钥密钥密钥密钥密钥***"}, // <=12 bytes? Chinese chars are 3 bytes each, so this exceeds 12 bytes and gets sliced by byte index.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maskKeyPrefix(tt.key)
			if tt.name == "unicode_multibyte" {
				// Byte-slicing a multi-byte UTF-8 string at a fixed byte offset can
				// split a rune. We only assert the invariant that matters for the
				// business behaviour (the masking suffix is present and the raw key
				// is never returned unmasked) rather than depending on exact bytes.
				if !strings.HasSuffix(got, "***") {
					t.Errorf("maskKeyPrefix(%q) = %q, want suffix ***", tt.key, got)
				}
				if got == tt.key {
					t.Errorf("maskKeyPrefix(%q) returned the raw key unmasked", tt.key)
				}
				return
			}
			if got != tt.want {
				t.Errorf("maskKeyPrefix(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

// ─── channelStatusLabel ─────────────────────────────────────────────────────

func TestHandlerRelay_ChannelStatusLabel_TableDriven(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   string
	}{
		{"enabled", common.ChannelStatusEnabled, "enabled"},
		{"auto_disabled", common.ChannelStatusAutoDisabled, "auto_disabled"},
		{"manually_disabled", common.ChannelStatusManuallyDisabled, "manually_disabled"},
		{"unknown_zero", 0, "unknown"},
		{"unknown_negative", -1, "unknown"},
		{"unknown_large", 999, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := channelStatusLabel(tt.status); got != tt.want {
				t.Errorf("channelStatusLabel(%d) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

// ─── resolvePlaygroundBaseURL ───────────────────────────────────────────────

func TestHandlerRelay_ResolvePlaygroundBaseURL(t *testing.T) {
	prevOverride := playgroundUpstreamBaseURL
	prevPort, hadPort := os.LookupEnv("PORT")
	t.Cleanup(func() {
		playgroundUpstreamBaseURL = prevOverride
		if hadPort {
			os.Setenv("PORT", prevPort)
		} else {
			os.Unsetenv("PORT")
		}
	})

	t.Run("override_wins_over_env", func(t *testing.T) {
		playgroundUpstreamBaseURL = "http://mock-upstream:9999"
		os.Setenv("PORT", "5555")
		if got := resolvePlaygroundBaseURL(); got != "http://mock-upstream:9999" {
			t.Errorf("resolvePlaygroundBaseURL() = %q, want override to win", got)
		}
	})

	t.Run("env_port_used_when_no_override", func(t *testing.T) {
		playgroundUpstreamBaseURL = ""
		os.Setenv("PORT", "8123")
		if got := resolvePlaygroundBaseURL(); got != "http://127.0.0.1:8123" {
			t.Errorf("resolvePlaygroundBaseURL() = %q, want http://127.0.0.1:8123", got)
		}
	})

	t.Run("default_3000_when_nothing_set", func(t *testing.T) {
		playgroundUpstreamBaseURL = ""
		os.Unsetenv("PORT")
		if got := resolvePlaygroundBaseURL(); got != "http://127.0.0.1:3000" {
			t.Errorf("resolvePlaygroundBaseURL() = %q, want http://127.0.0.1:3000 default", got)
		}
	})
}

// ─── GetOpenRouterApiPoolStatus ─────────────────────────────────────────────

// TestHandlerRelay_GetOpenRouterApiPoolStatus_MultiKeySnapshot seeds one
// multi-key OpenRouter channel (3 keys spanning enabled / cooling / permanent
// -disabled) plus two decoys that must be excluded from the snapshot: a
// single-key OpenRouter channel and a multi-key channel of a different
// vendor. This exercises the full per-key status classification, the
// cooldown-remaining clamp for an already-expired cooldown, and the
// tenant-blind SQL-level filter in repo.ListOpenRouterMultiKeyChannels.
func TestHandlerRelay_GetOpenRouterApiPoolStatus_MultiKeySnapshot(t *testing.T) {
	db, cleanup := handlerRelaySetupDB(t)
	defer cleanup()

	now := time.Now().Unix()
	multiKey := &repo.Channel{
		Name:   "or-pool-primary",
		Type:   constant.ChannelTypeOpenRouter,
		Key:    "sk-or-v1-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\nsk-or-v1-BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB\nsk-or-v1-CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC",
		Status: common.ChannelStatusEnabled,
		ChannelInfo: entity.ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: 3,
			// key 0: no entry -> enabled (default)
			MultiKeyStatusList: map[int]int{
				1: common.ChannelStatusAutoDisabled, // cooling (has future cooldown below)
				2: common.ChannelStatusAutoDisabled, // permanent (no cooldown entry)
			},
			MultiKeyCooldownUntil: map[int]int64{
				1: now + 120, // future -> cooling with positive remaining
			},
			MultiKeyDisabledReason: map[int]string{
				2: "manual ban",
			},
			MultiKeyDisabledTime: map[int]int64{
				2: now - 10,
			},
		},
	}
	if err := db.Create(multiKey).Error; err != nil {
		t.Fatalf("seed multiKey channel: %v", err)
	}

	// Decoy 1: OpenRouter but single-key -> must be excluded (IsMultiKey=false).
	singleKey := &repo.Channel{
		Name:   "or-single-decoy",
		Type:   constant.ChannelTypeOpenRouter,
		Key:    "sk-or-v1-single-key-only",
		Status: common.ChannelStatusEnabled,
	}
	if err := db.Create(singleKey).Error; err != nil {
		t.Fatalf("seed singleKey decoy: %v", err)
	}

	// Decoy 2: multi-key but not OpenRouter -> excluded by the SQL type filter.
	otherVendor := &repo.Channel{
		Name:   "openai-multikey-decoy",
		Type:   1, // OpenAI
		Key:    "sk-a\nsk-b",
		Status: common.ChannelStatusEnabled,
		ChannelInfo: entity.ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: 2,
		},
	}
	if err := db.Create(otherVendor).Error; err != nil {
		t.Fatalf("seed otherVendor decoy: %v", err)
	}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/openrouter-sync/api-pool", nil)

	GetOpenRouterApiPoolStatus(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	assertSuccess(t, resp)

	data, ok := resp["data"].([]interface{})
	if !ok {
		t.Fatalf("data field missing or wrong shape: %v", resp["data"])
	}
	if len(data) != 1 {
		t.Fatalf("data len = %d, want 1 (decoys must be excluded), body: %s", len(data), w.Body.String())
	}

	snap := data[0].(map[string]interface{})
	if snap["channel_name"] != "or-pool-primary" {
		t.Errorf("channel_name = %v, want or-pool-primary", snap["channel_name"])
	}
	if snap["status"] != "enabled" {
		t.Errorf("channel status = %v, want enabled", snap["status"])
	}
	if int(snap["key_count"].(float64)) != 3 {
		t.Errorf("key_count = %v, want 3", snap["key_count"])
	}
	if int(snap["enabled_count"].(float64)) != 1 {
		t.Errorf("enabled_count = %v, want 1", snap["enabled_count"])
	}
	if int(snap["cooling_count"].(float64)) != 1 {
		t.Errorf("cooling_count = %v, want 1", snap["cooling_count"])
	}
	if int(snap["permanent_disabled_count"].(float64)) != 1 {
		t.Errorf("permanent_disabled_count = %v, want 1", snap["permanent_disabled_count"])
	}

	keys := snap["keys"].([]interface{})
	if len(keys) != 3 {
		t.Fatalf("keys len = %d, want 3", len(keys))
	}

	k0 := keys[0].(map[string]interface{})
	if k0["status"] != "enabled" {
		t.Errorf("key0 status = %v, want enabled", k0["status"])
	}
	if k0["key_prefix"] != "sk-or-v1-AAA***" {
		t.Errorf("key0 key_prefix = %v, want masked prefix (raw key must never appear)", k0["key_prefix"])
	}

	k1 := keys[1].(map[string]interface{})
	if k1["status"] != "cooling" {
		t.Errorf("key1 status = %v, want cooling", k1["status"])
	}
	remaining, _ := k1["cooldown_seconds_remaining"].(float64)
	if remaining <= 0 || remaining > 120 {
		t.Errorf("key1 cooldown_seconds_remaining = %v, want in (0,120]", k1["cooldown_seconds_remaining"])
	}

	k2 := keys[2].(map[string]interface{})
	if k2["status"] != "permanent_disabled" {
		t.Errorf("key2 status = %v, want permanent_disabled", k2["status"])
	}
	if k2["disable_reason"] != "manual ban" {
		t.Errorf("key2 disable_reason = %v, want 'manual ban'", k2["disable_reason"])
	}
	if k2["key_prefix"] != "sk-or-v1-CCC***" {
		t.Errorf("key2 key_prefix = %v, want masked", k2["key_prefix"])
	}
}

// TestHandlerRelay_GetOpenRouterApiPoolStatus_EmptyPool covers the zero-
// channel case: response must still be success=true with an empty (not nil-
// rendered-as-null-breaking-clients) data array.
func TestHandlerRelay_GetOpenRouterApiPoolStatus_EmptyPool(t *testing.T) {
	_, cleanup := handlerRelaySetupDB(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/openrouter-sync/api-pool", nil)

	GetOpenRouterApiPoolStatus(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	resp := parseResponse(t, w)
	assertSuccess(t, resp)
	data, ok := resp["data"].([]interface{})
	if !ok {
		t.Fatalf("data missing/wrong type: %v", resp["data"])
	}
	if len(data) != 0 {
		t.Errorf("data len = %d, want 0", len(data))
	}
}

// ─── processChannelError ────────────────────────────────────────────────────

// TestHandlerRelay_ProcessChannelError_RecordsErrorLog pins the synchronous
// side effect of processChannelError: when error-log recording is enabled and
// the error is markable, a Log row is written with the governance fields
// (tenant, model, channel, token) sourced from gin context — this is the only
// audit trail an operator has for "why did channel #N fail". If this were a
// no-op, the assertions below would find zero rows.
func TestHandlerRelay_ProcessChannelError_RecordsErrorLog(t *testing.T) {
	db, cleanup := handlerRelaySetupDB(t)
	defer cleanup()

	prevErrorLogEnabled := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = true
	t.Cleanup(func() { constant.ErrorLogEnabled = prevErrorLogEnabled })

	c, _ := newTestCtx()
	c.Set("id", 55)
	c.Set("token_name", "my-relay-token")
	c.Set("original_model", "gpt-4o")
	c.Set("token_id", 77)
	c.Set("group", "default")
	c.Set("channel_id", 9)
	c.Set("channel_name", "primary-openai")
	c.Set("channel_type", 1) // not OpenRouter -> MaybeMarkCooldown goroutine no-ops immediately
	c.Set("tenant_id", "acme-corp")

	chErr := *types.NewChannelError(9, 1, "primary-openai", false, "sk-test-key", false)
	apiErr := types.NewErrorWithStatusCode(errors.New("upstream exploded with a 500"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)

	processChannelError(c, chErr, apiErr)

	var logs []repo.Log
	if err := db.Where("type = ?", repo.LogTypeError).Find(&logs).Error; err != nil {
		t.Fatalf("query error logs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("error log rows = %d, want 1, err was IsRecordErrorLog-eligible by default", len(logs))
	}
	lg := logs[0]
	if lg.UserId != 55 {
		t.Errorf("UserId = %d, want 55", lg.UserId)
	}
	if lg.TenantId != "acme-corp" {
		t.Errorf("TenantId = %q, want acme-corp", lg.TenantId)
	}
	if lg.ModelName != "gpt-4o" {
		t.Errorf("ModelName = %q, want gpt-4o", lg.ModelName)
	}
	if lg.ChannelId != 9 {
		t.Errorf("ChannelId = %d, want 9", lg.ChannelId)
	}
	if lg.TokenId != 77 {
		t.Errorf("TokenId = %d, want 77", lg.TokenId)
	}
	if lg.TokenName != "my-relay-token" {
		t.Errorf("TokenName = %q, want my-relay-token", lg.TokenName)
	}
	if !strings.Contains(lg.Content, "upstream exploded with a 500") {
		t.Errorf("Content = %q, want it to contain the upstream error message", lg.Content)
	}
	if !strings.Contains(lg.Other, `"status_code":500`) {
		t.Errorf("Other = %q, want it to record status_code 500", lg.Other)
	}
}

// TestHandlerRelay_ProcessChannelError_DisabledSkipsLogWrite verifies the
// governing flag actually gates the write: with ErrorLogEnabled=false, no
// row is inserted even for the same otherwise-eligible error.
func TestHandlerRelay_ProcessChannelError_DisabledSkipsLogWrite(t *testing.T) {
	db, cleanup := handlerRelaySetupDB(t)
	defer cleanup()

	prevErrorLogEnabled := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = false
	t.Cleanup(func() { constant.ErrorLogEnabled = prevErrorLogEnabled })

	c, _ := newTestCtx()
	c.Set("id", 55)
	c.Set("channel_id", 9)
	c.Set("channel_type", 1)

	chErr := *types.NewChannelError(9, 1, "primary-openai", false, "sk-test-key", false)
	apiErr := types.NewErrorWithStatusCode(errors.New("boom"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)

	processChannelError(c, chErr, apiErr)

	var count int64
	db.Model(&repo.Log{}).Where("type = ?", repo.LogTypeError).Count(&count)
	if count != 0 {
		t.Errorf("error log rows = %d, want 0 when ErrorLogEnabled=false", count)
	}
}

// ─── RelayClaudeCountTokens / writeClaudeCountTokensError ─────────────────

func postClaudeCountTokens(body string) (*httptest.ResponseRecorder, *gin.Context) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return w, c
}

// TestHandlerRelay_ClaudeCountTokens_ValidationErrors pins the free,
// side-effect-free size-query contract described in relay_count_tokens.go's
// own doc comment: malformed/incomplete requests must be rejected BEFORE any
// billing-adjacent code runs, in the Anthropic error envelope shape (the SDK
// calling this endpoint only understands that shape).
func TestHandlerRelay_ClaudeCountTokens_ValidationErrors(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantMessage string
	}{
		{"missing_messages_field", `{"model":"claude-3-5-sonnet-20241022"}`, "field messages is required"},
		{"missing_model_field", `{"messages":[{"role":"user","content":"hi"}]}`, "field model is required"},
		{"empty_messages_array", `{"model":"claude-3-5-sonnet-20241022","messages":[]}`, "field messages is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, c := postClaudeCountTokens(tt.body)
			RelayClaudeCountTokens(c)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
			}
			resp := parseResponse(t, w)
			if resp["type"] != "error" {
				t.Errorf(`resp["type"] = %v, want "error"`, resp["type"])
			}
			errObj, ok := resp["error"].(map[string]interface{})
			if !ok {
				t.Fatalf("resp[error] missing or wrong shape: %v", resp["error"])
			}
			if errObj["message"] != tt.wantMessage {
				t.Errorf("error.message = %v, want %q", errObj["message"], tt.wantMessage)
			}
		})
	}
}

// TestHandlerRelay_ClaudeCountTokens_MalformedJSON exercises the JSON-decode
// failure path (as opposed to a well-formed-but-incomplete body).
func TestHandlerRelay_ClaudeCountTokens_MalformedJSON(t *testing.T) {
	w, c := postClaudeCountTokens(`{"model": "claude-3", "messages": [`) // truncated
	RelayClaudeCountTokens(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	if resp["type"] != "error" {
		t.Errorf(`resp["type"] = %v, want "error"`, resp["type"])
	}
	errObj := resp["error"].(map[string]interface{})
	msg, _ := errObj["message"].(string)
	if msg == "" {
		t.Error("error.message empty for malformed JSON body")
	}
}

func TestHandlerRelay_WriteClaudeCountTokensError_SkipsIfAlreadyWritten(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.JSON(http.StatusTeapot, gin.H{"already": "written"})

	writeClaudeCountTokensError(c, types.NewErrorWithStatusCode(errors.New("should not overwrite"), types.ErrorCodeInvalidRequest, http.StatusBadRequest))

	if w.Code != http.StatusTeapot {
		t.Errorf("status = %d, want unchanged 418 (must not overwrite an already-written response)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "already") {
		t.Errorf("body was overwritten: %s", w.Body.String())
	}
}

func TestHandlerRelay_WriteClaudeCountTokensError_ZeroStatusDefaultsTo500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	apiErr := types.NewErrorWithStatusCode(errors.New("no status set"), types.ErrorCodeInvalidRequest, 0)
	writeClaudeCountTokensError(c, apiErr)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 default when apiErr.StatusCode<=0", w.Code)
	}
}

// ─── Playground ──────────────────────────────────────────────────────────

// TestHandlerRelay_Playground_AccessTokenDenied pins the access-token guard's
// status code. The error used to be built without a status code, so
// types.NewError fell back to 500 and a pure permission refusal was
// indistinguishable from a server fault (and counted into the error rate); it
// is now 403. fix_playground_access_denied_test.go pins the status and the
// presence of the error envelope — this one also pins that the message names
// the actual cause.
func TestHandlerRelay_Playground_AccessTokenDenied(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	c.Set("use_access_token", true)

	Playground(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (a permission refusal must not be reported as a server fault) — body: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("resp[error] missing: %v", resp)
	}
	if !strings.Contains(fmt.Sprint(errObj["message"]), "access token") {
		t.Errorf("error.message = %v, want it to mention access token", errObj["message"])
	}
}

// TestHandlerRelay_Playground_UnknownUserLookupFails pins the fail-closed
// behaviour when GetUserCache can't find the user backing the session's "id"
// context key (e.g. a stale/tampered session cookie referencing a deleted
// user): the handler must surface an error response, not panic or silently
// proceed with a nil user cache.
func TestHandlerRelay_Playground_UnknownUserLookupFails(t *testing.T) {
	_, cleanup := handlerRelaySetupDB(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	c.Set("id", 999999) // no such user seeded

	Playground(c)

	if w.Code < 400 {
		t.Fatalf("status = %d, want an error status for an unresolvable user", w.Code)
	}
	resp := parseResponse(t, w)
	if _, ok := resp["error"]; !ok {
		t.Errorf("expected an error envelope, got: %v", resp)
	}
}

// ─── runOnePlaygroundModel edge cases (direct unit calls, bypassing HTTP) ──

type handlerRelayFanoutParams = struct {
	Temperature *float64 `json:"temperature"`
	TopP        *float64 `json:"top_p"`
	MaxTokens   *int     `json:"max_tokens"`
}

// TestHandlerRelay_RunOnePlaygroundModel_MarshalFailure exercises the
// MARSHAL_FAILED branch: an Inf temperature cannot come from JSON over the
// wire (JSON has no Infinity literal), so this is only reachable by
// constructing the params in-process — which is exactly the kind of
// programmer-error / library-boundary edge a table-driven HTTP test could
// never reach.
func TestHandlerRelay_RunOnePlaygroundModel_MarshalFailure(t *testing.T) {
	inf := math.Inf(1)
	params := handlerRelayFanoutParams{Temperature: &inf}
	res := runOnePlaygroundModel(context.Background(), "http://example.invalid", "tok", "gpt-4o",
		[]playgroundChatMessage{{Role: "user", Content: "hi"}}, params)

	if res.ErrorCode != "MARSHAL_FAILED" {
		t.Errorf("ErrorCode = %q, want MARSHAL_FAILED", res.ErrorCode)
	}
	if res.ErrorMessage == "" {
		t.Error("ErrorMessage empty for a marshal failure")
	}
	if res.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o (must echo back even on failure)", res.Model)
	}
}

// TestHandlerRelay_RunOnePlaygroundModel_RelayFailed exercises the
// connection-refused path: baseURL points at a server that has already been
// closed, so the HTTP client's Do() itself fails.
func TestHandlerRelay_RunOnePlaygroundModel_RelayFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := srv.URL
	srv.Close() // now nothing is listening

	res := runOnePlaygroundModel(context.Background(), deadURL, "tok", "gpt-4o",
		[]playgroundChatMessage{{Role: "user", Content: "hi"}}, handlerRelayFanoutParams{})

	if res.ErrorCode != "RELAY_FAILED" {
		t.Errorf("ErrorCode = %q, want RELAY_FAILED, message=%q", res.ErrorCode, res.ErrorMessage)
	}
	if res.LatencyMs < 0 {
		t.Errorf("LatencyMs = %d, want >= 0", res.LatencyMs)
	}
}

// TestHandlerRelay_RunOnePlaygroundModel_ParseFailedTruncatesLongBody covers
// PARSE_FAILED when the upstream returns non-JSON garbage, and asserts the
// 500-byte truncation guard so a misbehaving upstream (e.g. an nginx 502 HTML
// page) can never stuff megabytes into the response envelope.
func TestHandlerRelay_RunOnePlaygroundModel_ParseFailedTruncatesLongBody(t *testing.T) {
	longGarbage := strings.Repeat("X", 800)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(longGarbage))
	}))
	defer srv.Close()

	res := runOnePlaygroundModel(context.Background(), srv.URL, "tok", "gpt-4o",
		[]playgroundChatMessage{{Role: "user", Content: "hi"}}, handlerRelayFanoutParams{})

	if res.ErrorCode != "PARSE_FAILED" {
		t.Fatalf("ErrorCode = %q, want PARSE_FAILED", res.ErrorCode)
	}
	if len(res.ErrorMessage) != len("X")*500+len("…(truncated)") {
		t.Errorf("ErrorMessage len = %d, want exactly 500 chars + truncation marker", len(res.ErrorMessage))
	}
	if !strings.HasSuffix(res.ErrorMessage, "…(truncated)") {
		t.Errorf("ErrorMessage = %q, want it to end with the truncation marker", res.ErrorMessage)
	}
}

// TestHandlerRelay_RunOnePlaygroundModel_UpstreamErrorDefaultCode covers the
// branch where the upstream's error envelope omits a `code` field — the
// result must still be marked as failed (not silently treated as success
// with empty content), falling back to a generic UPSTREAM_ERROR code.
func TestHandlerRelay_RunOnePlaygroundModel_UpstreamErrorDefaultCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"error":{"message":"no code field here"}}`))
	}))
	defer srv.Close()

	res := runOnePlaygroundModel(context.Background(), srv.URL, "tok", "gpt-4o",
		[]playgroundChatMessage{{Role: "user", Content: "hi"}}, handlerRelayFanoutParams{})

	if res.ErrorCode != "UPSTREAM_ERROR" {
		t.Errorf("ErrorCode = %q, want UPSTREAM_ERROR fallback", res.ErrorCode)
	}
	if res.ErrorMessage != "no code field here" {
		t.Errorf("ErrorMessage = %q, want the upstream message forwarded verbatim", res.ErrorMessage)
	}
	if res.Content != "" {
		t.Errorf("Content = %q, want empty on upstream error", res.Content)
	}
}

// ─── runChatTurn edge cases (v2_chat.go) ───────────────────────────────────

func TestHandlerRelay_RunChatTurn_MarshalFailure(t *testing.T) {
	inf := math.Inf(-1)
	params := handlerRelayFanoutParams{Temperature: &inf}
	res := runChatTurn(context.Background(), "http://example.invalid", "tok", "gpt-4o",
		[]playgroundChatMessage{{Role: "user", Content: "hi"}}, params)

	if res.ErrorCode != "MARSHAL_FAILED" {
		t.Errorf("ErrorCode = %q, want MARSHAL_FAILED", res.ErrorCode)
	}
}

func TestHandlerRelay_RunChatTurn_ParseFailedShortBodyNotTruncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json at all"))
	}))
	defer srv.Close()

	res := runChatTurn(context.Background(), srv.URL, "tok", "gpt-4o",
		[]playgroundChatMessage{{Role: "user", Content: "hi"}}, handlerRelayFanoutParams{})

	if res.ErrorCode != "PARSE_FAILED" {
		t.Fatalf("ErrorCode = %q, want PARSE_FAILED", res.ErrorCode)
	}
	if res.ErrorMessage != "not json at all" {
		t.Errorf("ErrorMessage = %q, want verbatim short body (no truncation under 500 chars)", res.ErrorMessage)
	}
}

func TestHandlerRelay_RunChatTurn_HappyPathTokenUsageForwarded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}],"usage":{"prompt_tokens":3,"completion_tokens":7}}`))
	}))
	defer srv.Close()

	res := runChatTurn(context.Background(), srv.URL, "tok", "gpt-4o",
		[]playgroundChatMessage{{Role: "user", Content: "ping"}}, handlerRelayFanoutParams{})

	if res.ErrorCode != "" {
		t.Fatalf("unexpected error: %s / %s", res.ErrorCode, res.ErrorMessage)
	}
	if res.Content != "pong" {
		t.Errorf("Content = %q, want pong", res.Content)
	}
	if res.PromptTokens != 3 || res.CompletionTokens != 7 {
		t.Errorf("tokens = (%d,%d), want (3,7)", res.PromptTokens, res.CompletionTokens)
	}
}

