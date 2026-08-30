package middleware

// Middleware-stage rejections (distributor model-not-found, authz, parse
// failures) used to leave no error-log row at all — live-probed 2026-08-30:
// with ERROR_LOG_ENABLED=true a request for a nonexistent model returned 404
// and the logs table stayed empty. These tests pin the fix: authenticated
// aborts through abortWithOpenAiMessage now record, while the two deliberate
// flood exclusions (anonymous callers, rate-limit 429s) stay out.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
)

// setupErrorLogDB layers what recordMiddlewareErrorLog needs on top of
// setupCoverDB: the Log table, repo.LOG_DB (setupCoverDB only swaps repo.DB),
// and ErrorLogEnabled=true. Cleanup restores all of it.
func setupErrorLogDB(t *testing.T) func() {
	t.Helper()
	db, coverCleanup := setupCoverDB(t)
	if err := db.AutoMigrate(&repo.Log{}); err != nil && !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("migrate Log: %v", err)
	}
	prevLogDB := repo.LOG_DB
	repo.LOG_DB = db
	prevEnabled := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = true
	return func() {
		constant.ErrorLogEnabled = prevEnabled
		repo.LOG_DB = prevLogDB
		coverCleanup()
	}
}

func countErrorLogRows(t *testing.T) int64 {
	t.Helper()
	var n int64
	if err := repo.LOG_DB.Model(&repo.Log{}).Where("type = ?", repo.LogTypeError).Count(&n).Error; err != nil {
		t.Fatalf("count error logs: %v", err)
	}
	return n
}

// TestDistribute_NoChannel_RecordsErrorLog is the end-to-end pin for the live
// gap: an AUTHENTICATED request for a model that was never configured is
// rejected by the distributor (404) and must leave a queryable error-log row
// whose model column names the model the client asked for — the row is the
// only trace the request leaves, since it never reaches the relay handler.
func TestDistribute_NoChannel_RecordsErrorLog(t *testing.T) {
	cleanup := setupErrorLogDB(t)
	defer cleanup()

	r := mountDistribute(func(c *gin.Context) {
		c.Set("id", 55)
		c.Set("token_id", 77)
		c.Set("token_name", "probe-token")
		c.Set("group", "default")
		c.Set("tenant_id", "acme-corp")
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	})
	w := doDistribute(r, `{"model":"no-such-model"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}

	var logs []repo.Log
	if err := repo.LOG_DB.Where("type = ?", repo.LogTypeError).Find(&logs).Error; err != nil {
		t.Fatalf("query error logs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("error log rows = %d, want 1", len(logs))
	}
	lg := logs[0]
	if lg.UserId != 55 {
		t.Errorf("UserId = %d, want 55", lg.UserId)
	}
	if lg.TenantId != "acme-corp" {
		t.Errorf("TenantId = %q, want acme-corp", lg.TenantId)
	}
	// Depends on Distribute publishing original_model BEFORE channel
	// selection — with the late (success-path-only) set this column is empty.
	if lg.ModelName != "no-such-model" {
		t.Errorf("ModelName = %q, want no-such-model", lg.ModelName)
	}
	if lg.TokenId != 77 {
		t.Errorf("TokenId = %d, want 77", lg.TokenId)
	}
	if !strings.Contains(lg.Content, "no-such-model") {
		t.Errorf("Content = %q, want the rejected model name in the message", lg.Content)
	}
	if !strings.Contains(lg.Other, `"status_code":404`) {
		t.Errorf("Other = %q, want status_code 404", lg.Other)
	}
	if !strings.Contains(lg.Other, `"stage":"middleware"`) {
		t.Errorf("Other = %q, want stage=middleware marker", lg.Other)
	}
}

// TestAbortWithOpenAiMessage_Anonymous_NoRow: a caller that never
// authenticated (no user id in context — e.g. an invalid-key 401) must not be
// able to turn probe spam into DB inserts.
func TestAbortWithOpenAiMessage_Anonymous_NoRow(t *testing.T) {
	cleanup := setupErrorLogDB(t)
	defer cleanup()

	c, _ := newTestContext(http.MethodPost, "/v1/chat/completions", `{}`, "application/json")
	abortWithOpenAiMessage(c, http.StatusUnauthorized, "invalid token")

	if n := countErrorLogRows(t); n != 0 {
		t.Errorf("error log rows = %d, want 0 for anonymous abort", n)
	}
}

// TestAbortWithOpenAiMessage_RateLimit429_NoRow: every rate limiter rejects
// through this helper; its rejection path must stay a cheap in-memory/Redis
// operation. A burst hitting the limiter must not fan out into one DB insert
// per rejected request.
func TestAbortWithOpenAiMessage_RateLimit429_NoRow(t *testing.T) {
	cleanup := setupErrorLogDB(t)
	defer cleanup()

	c, _ := newTestContext(http.MethodPost, "/v1/chat/completions", `{}`, "application/json")
	c.Set("id", 55)
	abortWithOpenAiMessage(c, http.StatusTooManyRequests, "rate limited")

	if n := countErrorLogRows(t); n != 0 {
		t.Errorf("error log rows = %d, want 0 for 429", n)
	}
}

// TestAbortWithOpenAiMessage_Disabled_NoRow: the ERROR_LOG_ENABLED master
// switch gates the middleware-stage write exactly like the relay-stage one.
func TestAbortWithOpenAiMessage_Disabled_NoRow(t *testing.T) {
	cleanup := setupErrorLogDB(t)
	defer cleanup()
	constant.ErrorLogEnabled = false

	c, _ := newTestContext(http.MethodPost, "/v1/chat/completions", `{}`, "application/json")
	c.Set("id", 55)
	abortWithOpenAiMessage(c, http.StatusNotFound, "model gone")

	if n := countErrorLogRows(t); n != 0 {
		t.Errorf("error log rows = %d, want 0 when ErrorLogEnabled=false", n)
	}
}
