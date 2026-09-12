package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestSetApiRouter_TotpWiring asserts the TOTP step-up factor endpoints are
// mounted to match the frontend contract (web/src/services/secureVerification.js
// TotpService): status/enroll/confirm/disable under /api/user/totp/*.
func TestSetApiRouter_TotpWiring(t *testing.T) {
	common.RedisEnabled = false
	gin.SetMode(gin.TestMode)

	engine := gin.New()
	SetApiRouter(engine)

	has := func(method, path string) bool {
		for _, rt := range engine.Routes() {
			if rt.Method == method && rt.Path == path {
				return true
			}
		}
		return false
	}

	for _, c := range []struct{ method, path string }{
		{"GET", "/api/user/totp/status"},
		{"POST", "/api/user/totp/enroll"},
		{"POST", "/api/user/totp/confirm"},
		{"POST", "/api/user/totp/disable"},
		{"POST", "/api/user/totp/backup-codes/regenerate"},
	} {
		if !has(c.method, c.path) {
			t.Errorf("route %s %s not registered", c.method, c.path)
		}
	}
}

var totpBackupRateLimitDBCounter atomic.Int64

// TestSetApiRouter_TotpBackupCodesRegenerate_OwnRateLimitBucket is the
// oracle for the §8 L6 amendment: POST /api/user/totp/backup-codes/regenerate
// must be throttled by its own "TB" bucket (middleware.TotpBackupCodesRateLimit,
// 5/20min), NOT CriticalRateLimit's shared "CT" bucket used by
// /user/totp/disable and /channel/:id/key — a burst against one must not
// starve the other. Without this test, swapping the regenerate route's
// middleware back to middleware.CriticalRateLimit() in api-router.go left
// every other test in the repo green (grep: no test referenced
// TotpBackupCodesRateLimit or the "TB" mark before this one).
//
// Drives the REAL SetApiRouter (not a hand-mounted mimic), same convention
// as root_seal_test.go: a fresh gin.Engine per request with the session
// identity injected directly (no login round-trip needed since UserAuth
// re-reads the session on every request), and a unique fake IP per test run
// so the middleware package's process-wide in-memory rate-limit buckets
// (keyed by mark+IP) never carry state over from another test or a previous
// run of this one.
//
// Mutation proof: temporarily changing api-router.go's regenerate route to
// use CriticalRateLimit() instead of TotpBackupCodesRateLimit() makes the
// disable-route assertion below fail (429 instead of the expected
// VERIFICATION_REQUIRED 403), because the 5 regenerate calls exhaust the
// now-shared "CT" bucket (temporarily capped at 5 for this test) before
// disable ever gets a turn.
func TestSetApiRouter_TotpBackupCodesRegenerate_OwnRateLimitBucket(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:totp_tb_bucket_%d?mode=memory&cache=shared", totpBackupRateLimitDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&repo.User{}); err != nil {
		t.Fatalf("migrate User: %v", err)
	}

	prevDB, prevRedis := repo.DB, common.RedisEnabled
	repo.DB = db
	repo.InitCol()
	common.RedisEnabled = false
	// Shrink the shared "CT" bucket's budget to 5 for the duration of this
	// test so the mutation described above is caught deterministically
	// (with the default of 20, 6 CT-bucket hits would not exhaust it and
	// the disable-route assertion could false-pass under the mutation).
	prevCTEnable, prevCTNum := common.CriticalRateLimitEnable, common.CriticalRateLimitNum
	common.CriticalRateLimitEnable = true
	common.CriticalRateLimitNum = 5
	t.Cleanup(func() {
		repo.DB, common.RedisEnabled = prevDB, prevRedis
		common.CriticalRateLimitEnable, common.CriticalRateLimitNum = prevCTEnable, prevCTNum
		if sqlDB, dbErr := db.DB(); dbErr == nil && sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	user := &repo.User{
		Username: "tb-bucket-user", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: "tb-bucket@local", TenantId: "default",
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Unique fake source IP for this test run so its rate-limit buckets
	// never collide with another test (or a repeat run) hitting the same
	// mark from the httptest-default client address.
	fakeIP := fmt.Sprintf("10.77.%d.1", totpBackupRateLimitDBCounter.Load()%250)

	serve := func(method, path string) *httptest.ResponseRecorder {
		engine := gin.New()
		engine.Use(gin.Recovery())
		store := cookie.NewStore([]byte("tb-bucket-test-secret"))
		engine.Use(sessions.Sessions("session", store))
		engine.Use(func(c *gin.Context) {
			s := sessions.Default(c)
			s.Set("username", user.Username)
			s.Set("role", user.Role)
			s.Set("id", user.Id)
			s.Set("status", common.UserStatusEnabled)
			_ = s.Save()
			c.Next()
		})
		SetApiRouter(engine)

		req := httptest.NewRequest(method, path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = fakeIP + ":1234"
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	// 5 calls to regenerate must all get past the rate limiter (none of them
	// are 429) — they fail later at SecureVerificationRequired
	// (VERIFICATION_REQUIRED, no step-up cookie was ever obtained), which is
	// fine: this test only cares what the LIMITER did.
	for i := 0; i < 5; i++ {
		w := serve(http.MethodPost, "/api/user/totp/backup-codes/regenerate")
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("regenerate call %d already 429 — TB bucket should allow 5 before tripping (body=%s)", i+1, w.Body.String())
		}
	}
	// The 6th regenerate call must trip the TB bucket's own 5-request cap.
	w6 := serve(http.MethodPost, "/api/user/totp/backup-codes/regenerate")
	if w6.Code != http.StatusTooManyRequests {
		t.Fatalf("6th regenerate call: status=%d, want 429 (TB bucket must cap at 5); body=%s", w6.Code, w6.Body.String())
	}

	// The disable route (CriticalRateLimit's "CT" bucket) must be untouched
	// by the 6 regenerate calls above — proving they landed in a separate
	// bucket, not the shared one.
	wDisable := serve(http.MethodPost, "/api/user/totp/disable")
	if wDisable.Code == http.StatusTooManyRequests {
		t.Fatalf("disable route got 429 after 6 regenerate calls — regenerate must not share CriticalRateLimit's \"CT\" bucket; body=%s", wDisable.Body.String())
	}
}
