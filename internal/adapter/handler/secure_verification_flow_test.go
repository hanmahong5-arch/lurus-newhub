package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

const channelTestSecretKey = "sk-upstream-secret-fixture"

// setupSecureVerifyFlowDB points the repo globals at a hermetic SQLite DB seeded
// with an enabled root user (id=1) and one channel (id=1) holding an upstream
// key. Returns a cleanup that restores the previous globals.
func setupSecureVerifyFlowDB(t *testing.T) func() {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:secverifyflow%d?mode=memory&cache=shared", testDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &repo.Channel{}, &repo.Log{}, &repo.Option{}} {
		if err := db.AutoMigrate(tbl); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("automigrate %T: %v", tbl, err)
		}
	}

	prevDB, prevLogDB := repo.DB, repo.LOG_DB
	prevSQLite, prevPG, prevRedis := common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled
	prevLogConsume := common.LogConsumeEnabled

	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	common.LogConsumeEnabled = false

	if err := db.Create(&repo.User{
		Id:       1,
		Username: "root",
		Role:     common.RoleRootUser,
		Status:   common.UserStatusEnabled,
		Email:    "root@test.local",
	}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := db.Create(&repo.Channel{
		Id:       1,
		TenantId: "default",
		Name:     "test-channel",
		Key:      channelTestSecretKey,
		Status:   1,
	}).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	return func() {
		repo.DB, repo.LOG_DB = prevDB, prevLogDB
		common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled = prevSQLite, prevPG, prevRedis
		common.LogConsumeEnabled = prevLogConsume
		if sqlDB, e := db.DB(); e == nil && sqlDB != nil {
			_ = sqlDB.Close()
		}
	}
}

// buildSecureVerifyFlowRouter wires the real UniversalVerify + GetChannelKey
// behind SecureVerificationRequired. A stub middleware stands in for
// RootAuth/UserAuth (covered by the middleware auth tests) so this test
// isolates the secure-verification layer.
func buildSecureVerifyFlowRouter() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	store := cookie.NewStore([]byte("secure-verify-flow-secret"))
	r.Use(sessions.Sessions("session", store))
	r.Use(func(c *gin.Context) {
		c.Set("id", 1)
		c.Next()
	})
	r.POST("/api/verify", UniversalVerify)
	r.POST("/api/channel/:id/key", middleware.SecureVerificationRequired(), GetChannelKey)
	return r
}

// TestSecureVerificationFlow_ChannelKeyReveal_EndToEnd proves the wired feature
// behaves as the frontend expects: a reveal without verification is refused with
// VERIFICATION_REQUIRED, POST /api/verify stamps the session, and a reveal
// carrying that session returns the channel key.
func TestSecureVerificationFlow_ChannelKeyReveal_EndToEnd(t *testing.T) {
	cleanup := setupSecureVerifyFlowDB(t)
	defer cleanup()
	r := buildSecureVerifyFlowRouter()

	// 1. Reveal without prior verification -> 403 VERIFICATION_REQUIRED.
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, httptest.NewRequest(http.MethodPost, "/api/channel/1/key", nil))
	if w1.Code != http.StatusForbidden {
		t.Fatalf("pre-verify reveal: status=%d want 403; body=%s", w1.Code, w1.Body.String())
	}
	var e1 struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(w1.Body.Bytes(), &e1)
	if e1.Code != "VERIFICATION_REQUIRED" {
		t.Fatalf("pre-verify code=%q want VERIFICATION_REQUIRED", e1.Code)
	}

	// 2. Pass session verification -> 200 + a session cookie.
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/verify", strings.NewReader(`{"method":"session"}`))
	req2.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("verify: status=%d want 200; body=%s", w2.Code, w2.Body.String())
	}
	cookies := w2.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("verify did not set a session cookie")
	}

	// 3. Reveal carrying the verified session -> 200 + the upstream key.
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "/api/channel/1/key", nil)
	for _, ck := range cookies {
		req3.AddCookie(ck)
	}
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Fatalf("post-verify reveal: status=%d want 200; body=%s", w3.Code, w3.Body.String())
	}
	var ok struct {
		Success bool `json:"success"`
		Data    struct {
			Key string `json:"key"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w3.Body.Bytes(), &ok); err != nil {
		t.Fatalf("unmarshal reveal: %v", err)
	}
	if !ok.Success || ok.Data.Key != channelTestSecretKey {
		t.Fatalf("reveal: success=%v key=%q, want true %q", ok.Success, ok.Data.Key, channelTestSecretKey)
	}
}

// TestSecureVerificationFlow_ChannelKeyReveal_FlagOn_RefusedEndToEnd proves
// the REAL-CHAIN claim in secure_verification.go's doc comment: with
// SECURE_VERIFICATION_REQUIRE_ENROLLMENT=true, a no-enrollment user cannot
// obtain step-up verification through the real middleware.SecureVerificationRequired
// chain in front of channel-key reveal — not just through the status
// endpoint. This drives the real router built by buildSecureVerifyFlowRouter,
// the same one TestSecureVerificationFlow_ChannelKeyReveal_EndToEnd uses for
// the flag-off path.
func TestSecureVerificationFlow_ChannelKeyReveal_FlagOn_RefusedEndToEnd(t *testing.T) {
	cleanup := setupSecureVerifyFlowDB(t)
	defer cleanup()
	t.Setenv("SECURE_VERIFICATION_REQUIRE_ENROLLMENT", "true")
	r := buildSecureVerifyFlowRouter()

	// 1. POST /api/verify -> 403 STEP_UP_ENROLLMENT_REQUIRED, no session key set.
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/api/verify", strings.NewReader(`{"method":"session"}`))
	req1.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusForbidden {
		t.Fatalf("verify: status=%d want 403; body=%s", w1.Code, w1.Body.String())
	}
	var e1 struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(w1.Body.Bytes(), &e1)
	if e1.Code != "STEP_UP_ENROLLMENT_REQUIRED" {
		t.Fatalf("verify code=%q want STEP_UP_ENROLLMENT_REQUIRED", e1.Code)
	}

	// 2. Replay whatever cookies came back against the real
	// middleware.SecureVerificationRequired-gated route -> 403
	// VERIFICATION_REQUIRED (the middleware's own refusal, proving no
	// session key reached the store).
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/channel/1/key", nil)
	for _, ck := range w1.Result().Cookies() {
		req2.AddCookie(ck)
	}
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Fatalf("post-flagon reveal: status=%d want 403; body=%s", w2.Code, w2.Body.String())
	}
	var e2 struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &e2)
	if e2.Code != "VERIFICATION_REQUIRED" {
		t.Fatalf("post-flagon reveal code=%q want VERIFICATION_REQUIRED", e2.Code)
	}
}
