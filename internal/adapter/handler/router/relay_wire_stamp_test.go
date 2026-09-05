package router

// relay_wire_stamp_test.go proves middleware.StampRelayFormat() is actually
// mounted ahead of the rejecting middleware on the real /v1 and /v1beta
// relay chains — not merely that it exists somewhere in the tree. Same
// rationale as r6a_rate_limit_mount_test.go: this calls SetRelayRouter, the
// same function cmd/server/main.go reaches via SetRouter, so deleting one of
// the middleware.StampRelayFormat() Use() calls in relay-router.go would
// make this test fail while a hand-built engine in the middleware package
// (rejection_envelope_wire_test.go) would not catch it — that test mirrors
// the group layout by hand, this one drives the actual wiring.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var relayWireStampDBCounter atomic.Int64

// relayWireStampEmptyDB wires an isolated, empty in-memory SQLite DB into
// repo.DB — no token rows, so TokenAuth's ValidateUserToken lookup always
// misses and every probe in this file rejects with a genuine "bad key" 401,
// exactly the path abortWithOpenAiMessage/renderRejection now runs through.
func relayWireStampEmptyDB(t *testing.T) func() {
	t.Helper()
	seq := relayWireStampDBCounter.Add(1)
	dbName := fmt.Sprintf("file:relay_wire_stamp_%d?mode=memory&cache=shared", seq)
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&repo.User{}, &repo.Token{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	prevDB := repo.DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled
	repo.DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	return func() {
		repo.DB = prevDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		if sqlDB, dbErr := db.DB(); dbErr == nil && sqlDB != nil {
			_ = sqlDB.Close()
		}
	}
}

// TestRelayRouter_StampsWireBeforeTokenAuth hits /v1/messages and
// /v1beta/models/x:generateContent through the real SetRelayRouter wiring
// without a key. If StampRelayFormat were missing (or mounted after
// TokenAuth, where gin's snapshot-at-Group()-time semantics would make it
// too late), both rejections would fall back to the OpenAI envelope
// (asserted not to happen here) instead of their own wire's shape.
func TestRelayRouter_StampsWireBeforeTokenAuth(t *testing.T) {
	cleanup := relayWireStampEmptyDB(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRelayRouter(engine)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("/v1/messages no key: status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"type":"error"`) {
		t.Errorf(`/v1/messages 401 body = %s, want the Claude envelope ("type":"error") — StampRelayFormat must run before TokenAuth on relayV1Router`, body)
	}
	if strings.Contains(body, "new_api_error") {
		t.Errorf("/v1/messages 401 body = %s, must not be the OpenAI envelope", body)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/v1beta/models/x:generateContent", strings.NewReader(`{}`))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	engine.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("/v1beta/models/x:generateContent no key: status = %d, want 401; body=%s", w2.Code, w2.Body.String())
	}
	body2 := w2.Body.String()
	if !strings.HasPrefix(body2, `{"error":{"code":401`) {
		t.Errorf(`/v1beta 401 body = %s, want the Gemini envelope {"error":{"code":401,... — StampRelayFormat must run before TokenAuth on relayGeminiRouter`, body2)
	}
}
