package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/andybalholm/brotli"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
)

// mountWithSession builds an engine with a cookie session store that seeds the
// given user id before the target middleware runs.
func mountWithSession(userID int, method, path string, mw gin.HandlerFunc) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	store := cookie.NewStore([]byte("final-cover-secret"))
	r.Use(sessions.Sessions("session", store))
	r.Use(func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("id", userID)
		_ = s.Save()
		c.Next()
	})
	r.Handle(method, path, mw, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	return r
}

// -------------------- cost_spike breach path (redis) --------------------

func TestCostSpikeLimit_Breach_DisablesAnd429(t *testing.T) {
	_, dbCleanup := setupCoverDB(t)
	defer dbCleanup()
	redisCleanup := withRedis(t)
	defer redisCleanup()

	user := &repo.User{Username: "spiker", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Email: "s@local", TenantId: "default"}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	prevEnabled := common.CostSpikeProtectionEnabled
	prevLimit := common.CostSpikeHardLimitPer5Min
	common.CostSpikeProtectionEnabled = true
	common.CostSpikeHardLimitPer5Min = 0 // any usage (>=0) breaches immediately
	defer func() {
		common.CostSpikeProtectionEnabled = prevEnabled
		common.CostSpikeHardLimitPer5Min = prevLimit
	}()

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) { c.Set("id", user.Id); c.Next() })
	r.GET("/relay", CostSpikeLimit(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/relay", nil))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 for cost-spike breach; body=%s", w.Code, w.Body.String())
	}
	// The user must be auto-disabled as a side effect.
	got, err := repo.GetUserById(user.Id, false)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if got.Status != common.UserStatusDisabled {
		t.Errorf("user status = %d, want disabled after breach", got.Status)
	}
}

// -------------------- gzip: brotli + Close --------------------

func TestDecompressRequestMiddleware_Brotli_Decodes(t *testing.T) {
	var body []byte
	r := gin.New()
	r.POST("/x", DecompressRequestMiddleware(), func(c *gin.Context) {
		b, _ := io.ReadAll(c.Request.Body)
		body = b
		_ = c.Request.Body.Close() // trigger readCloser.Close (brotli closeFn)
		c.Status(http.StatusOK)
	})

	var buf bytes.Buffer
	bw := brotli.NewWriter(&buf)
	_, _ = bw.Write([]byte(`{"model":"claude"}`))
	_ = bw.Close()

	req := httptest.NewRequest(http.MethodPost, "/x", &buf)
	req.Header.Set("Content-Encoding", "br")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if string(body) != `{"model":"claude"}` {
		t.Errorf("brotli-decoded body = %q, want original JSON", string(body))
	}
}

func TestDecompressRequestMiddleware_Gzip_CloseInvoked(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, _ = gw.Write([]byte("payload"))
	_ = gw.Close()

	r := gin.New()
	r.POST("/x", DecompressRequestMiddleware(), func(c *gin.Context) {
		_, _ = io.ReadAll(c.Request.Body)
		_ = c.Request.Body.Close() // exercise the gzip readCloser.Close closeFn
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodPost, "/x", &buf)
	req.Header.Set("Content-Encoding", "gzip")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// -------------------- redis error paths (dead client → 500) --------------

func deadRedis(t *testing.T) func() {
	t.Helper()
	// A client pointed at a closed port; commands fail fast.
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 200 * time.Millisecond})
	prevRDB := common.RDB
	prevEnabled := common.RedisEnabled
	common.RDB = rdb
	common.RedisEnabled = true
	return func() {
		_ = rdb.Close()
		common.RDB = prevRDB
		common.RedisEnabled = prevEnabled
	}
}

func TestRedisRateLimiterKeyed_LLenError_500(t *testing.T) {
	cleanup := deadRedis(t)
	defer cleanup()
	c, w := newTestContext(http.MethodGet, "/x", "", "")
	redisRateLimiterKeyed(c, 5, 60, "ERR", "identX")
	if !c.IsAborted() || c.Writer.Status() != http.StatusInternalServerError {
		t.Errorf("aborted=%v status=%d, want abort 500 on Redis LLen failure", c.IsAborted(), c.Writer.Status())
	}
	_ = w
}

// TestModelRateLimit_Redis_CheckError_FailsOpen was TestModelRateLimit_Redis_CheckError_500
// until operator decision D1 (2026-08-27): checkRedisRateLimit's LLen error is
// now a deliberate fail-OPEN (redisRateLimitHandler calls c.Next() and admits
// the request), matching the sibling limiters BusinessRateLimit and
// RelayConcurrencyLimit. The 500 this test used to assert was reachable only
// because setting.ModelRequestRateLimitEnabled defaulted to false in every
// other test in this package/repo — i.e. this branch had never actually run
// against live traffic before this round armed the switch, so there is no
// "pre-fix passing behavior" being changed out from under production here.
// The replacement assertions: (1) the request is admitted (200, not
// aborted), and (2) metrics.RateLimitDegradedTotal for
// "model_rate_limit_success" increments — the degradation must stay visible
// even though it's no longer fatal.
func TestModelRateLimit_Redis_CheckError_FailsOpen(t *testing.T) {
	cleanup := deadRedis(t)
	defer cleanup()
	before := testutil.ToFloat64(metrics.RateLimitDegradedTotal.WithLabelValues("model_rate_limit_success"))
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("id", 999123); c.Next() })
	r.GET("/m", redisRateLimitHandler(60, 0, 5), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/m", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (fail-open on Redis error, operator decision D1)", w.Code)
	}
	after := testutil.ToFloat64(metrics.RateLimitDegradedTotal.WithLabelValues("model_rate_limit_success"))
	if after != before+1 {
		t.Errorf("RateLimitDegradedTotal{model_rate_limit_success} = %v, want %v (must increment on every fail-open, not just log)", after, before+1)
	}
}

// -------------------- TokenAuth: admin specific-channel via parts --------

func TestTokenAuth_AdminSpecificChannel(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	// Admin user + token whose key has no dashes → suffix "-7" selects channel 7.
	admin := &repo.User{Username: "adm", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Email: "adm@local", TenantId: "default"}
	if err := repo.DB.Create(admin).Error; err != nil {
		t.Fatalf("create admin: %v", err)
	}
	key := common.GetRandomString(48)
	tok := &repo.Token{UserId: admin.Id, TenantId: "default", Key: key, Status: common.TokenStatusEnabled, Name: "adm-tok", ExpiredTime: -1, UnlimitedQuota: true, CreatedTime: common.GetTimestamp(), AccessedTime: common.GetTimestamp()}
	if err := repo.DB.Create(tok).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}
	r := mountTokenAuth("/v1/chat/completions")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer sk-"+key+"-7")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (admin may pin channel); body=%s", w.Code, w.Body.String())
	}
}

func TestTokenAuth_CommonUserSpecificChannel_403(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	// Common user attempting the "-channel" suffix must be rejected.
	_, key, _ := seedUserToken(t)
	r := mountTokenAuth("/v1/chat/completions")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer sk-"+key+"-7")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (common user cannot pin channel); body=%s", w.Code, w.Body.String())
	}
}

// -------------------- TokenAuth: model-limit + metered quota context ------

func TestTokenAuth_ModelLimitAndMeteredQuota(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	user := &repo.User{Username: "mq", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Email: "mq@local", TenantId: "default"}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	key := common.GetRandomString(48)
	tok := &repo.Token{
		UserId: user.Id, TenantId: "default", Key: key, Status: common.TokenStatusEnabled, Name: "mq-tok",
		ExpiredTime: -1, UnlimitedQuota: false, RemainQuota: 500000,
		ModelLimitsEnabled: true, ModelLimits: "gpt-4o",
		CreatedTime: common.GetTimestamp(), AccessedTime: common.GetTimestamp(),
	}
	if err := repo.DB.Create(tok).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}
	r := mountTokenAuth("/v1/chat/completions")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer sk-"+key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (metered token with model limits); body=%s", w.Code, w.Body.String())
	}
}

// -------------------- PlaygroundAuth auto-create token --------------------

func TestPlaygroundAuth_SessionAutoCreatesToken(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	// User with NO tokens → PlaygroundAuth auto-creates a default one.
	user := &repo.User{Username: "pgnew", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Email: "pgnew@local", TenantId: "default", Quota: 1_000_000}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	r := mountWithSession(user.Id, http.MethodPost, "/pg", PlaygroundAuth())
	req := httptest.NewRequest(http.MethodPost, "/pg", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (auto-created default token); body=%s", w.Code, w.Body.String())
	}
	// A token should now exist for the user.
	toks, err := repo.GetAllUserTokens(user.Id, 0, 10)
	if err != nil || len(toks) == 0 {
		t.Errorf("expected an auto-created token, got %d (err=%v)", len(toks), err)
	}
}
