package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

// ============================ flex_auth ==================================

func createEnabledUserAndToken(t *testing.T) string {
	t.Helper()
	user := &repo.User{
		Username:    "flexuser",
		DisplayName: "Flex User",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Email:       "flex@local",
		TenantId:    "default",
		Quota:       1_000_000,
	}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	key := common.GetRandomString(48)
	token := &repo.Token{
		UserId:         user.Id,
		TenantId:       "default",
		Key:            key,
		Status:         common.TokenStatusEnabled,
		Name:           "flex-token",
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}
	if err := repo.DB.Create(token).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}
	return key
}

func mountFlex() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/flex", FlexAuth(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"id": c.GetInt("id"), "method": c.GetString("auth_method")})
	})
	return r
}

func TestFlexAuth_NoHeader_401(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	r := mountFlex()
	req := httptest.NewRequest(http.MethodGet, "/flex", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for missing Authorization", w.Code)
	}
}

func TestFlexAuth_InvalidToken_401(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	r := mountFlex()
	req := httptest.NewRequest(http.MethodGet, "/flex", nil)
	req.Header.Set("Authorization", "Bearer sk-doesnotexist")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for invalid API token", w.Code)
	}
}

func TestFlexAuth_ValidToken_Passes(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	key := createEnabledUserAndToken(t)
	r := mountFlex()
	req := httptest.NewRequest(http.MethodGet, "/flex", nil)
	req.Header.Set("Authorization", "Bearer sk-"+key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for valid token; body=%s", w.Code, w.Body.String())
	}
}

func TestFlexAuth_ValidToken_DisabledUser_403(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	// Create user disabled + token pointing at it.
	user := &repo.User{Username: "disflex", Role: common.RoleCommonUser, Status: common.UserStatusDisabled, Email: "d@local", TenantId: "default"}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	key := common.GetRandomString(48)
	tok := &repo.Token{UserId: user.Id, TenantId: "default", Key: key, Status: common.TokenStatusEnabled, Name: "t", ExpiredTime: -1, UnlimitedQuota: true, CreatedTime: common.GetTimestamp(), AccessedTime: common.GetTimestamp()}
	if err := repo.DB.Create(tok).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}
	r := mountFlex()
	req := httptest.NewRequest(http.MethodGet, "/flex", nil)
	req.Header.Set("Authorization", "Bearer sk-"+key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for disabled user; body=%s", w.Code, w.Body.String())
	}
}

func TestFlexAuth_JWTPath_OIDCDisabled_ServiceUnavailable(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	r := mountFlex()
	// Non-"sk-" bearer routes to flexAuthViaJWT → OIDCAuth. oidcEnabled is
	// false in unit scope, so OIDCAuth aborts 503. The point: FlexAuth must
	// NOT admit a non-token credential when JWT auth is unavailable.
	req := httptest.NewRequest(http.MethodGet, "/flex", nil)
	req.Header.Set("Authorization", "Bearer eyJhbGciOiJSUzI1NiJ9.fake.sig")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == http.StatusOK {
		t.Errorf("status = 200, want abort for JWT path with OIDC disabled")
	}
}

// ============================ tenant_scope ===============================

func mountTenantGuard(withTenantCtx *TenantContext) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		if withTenantCtx != nil {
			c.Set("tenant_context", withTenantCtx)
		}
		c.Next()
	})
	pass := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) }
	r.GET("/api/v2/:tenant_slug/x", TenantSlugGuard(), pass)
	r.GET("/api/v2/noslug", TenantSlugGuard(), pass) // slug-less
	return r
}

func seedTenant(t *testing.T, id, slug string, status int) {
	t.Helper()
	tn := &entity.Tenant{Id: id, Slug: slug, Name: slug, Status: status, PlanType: entity.TenantPlanFree, MaxUsers: 10, MaxQuota: 1000, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := repo.DB.Create(tn).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
}

func TestTenantSlugGuard_NoSlug_Passes(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	r := mountTenantGuard(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/noslug", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for slug-less route", w.Code)
	}
}

func TestTenantSlugGuard_TenantNotFound_404(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	r := mountTenantGuard(&TenantContext{TenantID: "t1"})
	req := httptest.NewRequest(http.MethodGet, "/api/v2/ghost/x", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for unknown slug", w.Code)
	}
}

func TestTenantSlugGuard_NoTenantContext_401(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	seedTenant(t, "t1", "acme", entity.TenantStatusEnabled)
	r := mountTenantGuard(nil) // no tenant_context set
	req := httptest.NewRequest(http.MethodGet, "/api/v2/acme/x", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 when tenant context missing", w.Code)
	}
}

func TestTenantSlugGuard_TenantMismatch_403(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	seedTenant(t, "tenantA", "acme", entity.TenantStatusEnabled)
	// Caller authenticated into tenantB but requests acme (tenantA) → cross-tenant.
	r := mountTenantGuard(&TenantContext{TenantID: "tenantB"})
	req := httptest.NewRequest(http.MethodGet, "/api/v2/acme/x", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for tenant mismatch (cross-tenant routing must be blocked)", w.Code)
	}
}

func TestTenantSlugGuard_TenantDisabled_403(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	seedTenant(t, "tenantA", "acme", entity.TenantStatusSuspended)
	r := mountTenantGuard(&TenantContext{TenantID: "tenantA"})
	req := httptest.NewRequest(http.MethodGet, "/api/v2/acme/x", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for suspended tenant", w.Code)
	}
}

func TestTenantSlugGuard_HappyPath(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	seedTenant(t, "tenantA", "acme", entity.TenantStatusEnabled)
	r := mountTenantGuard(&TenantContext{TenantID: "tenantA"})
	req := httptest.NewRequest(http.MethodGet, "/api/v2/acme/x", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for matching enabled tenant; body=%s", w.Code, w.Body.String())
	}
}

// ============================ entitlement ================================

func mountEntitlement(accountID interface{}) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		if accountID != nil {
			c.Set("identity_account_id", accountID)
		}
		c.Next()
	})
	r.GET("/e", EntitlementCheck(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	return r
}

func TestEntitlementCheck_NoAccountID_Passes(t *testing.T) {
	r := mountEntitlement(nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/e", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for legacy auth without account id", w.Code)
	}
}

func TestEntitlementCheck_NonInt64AccountID_Passes(t *testing.T) {
	r := mountEntitlement("not-int64")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/e", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 when account id is wrong type", w.Code)
	}
}

func TestEntitlementCheck_NonPositiveAccountID_Passes(t *testing.T) {
	r := mountEntitlement(int64(0))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/e", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for non-positive account id", w.Code)
	}
}

func TestEntitlementCheck_CacheHitAllowed_Passes(t *testing.T) {
	const acct = int64(555001)
	key := entitlementCacheKey{accountID: acct, product: ratio_setting.DefaultSourceProduct}
	entitlementCache.Store(key, entitlementEntry{allowed: true, checkedAt: time.Now()})
	defer entitlementCache.Delete(key)
	r := mountEntitlement(acct)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/e", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for fresh allowed cache entry", w.Code)
	}
}

func TestEntitlementCheck_CacheHitDenied_429(t *testing.T) {
	const acct = int64(555002)
	key := entitlementCacheKey{accountID: acct, product: ratio_setting.DefaultSourceProduct}
	entitlementCache.Store(key, entitlementEntry{allowed: false, checkedAt: time.Now()})
	defer entitlementCache.Delete(key)
	r := mountEntitlement(acct)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/e", nil))
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429 for exhausted quota cache entry", w.Code)
	}
}

// ============================ cost_spike =================================

func mountCostSpike(userID int) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		if userID != 0 {
			c.Set("id", userID)
		}
		c.Next()
	})
	r.GET("/cs", CostSpikeLimit(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	return r
}

func TestCostSpikeLimit_Disabled_Passes(t *testing.T) {
	prev := common.CostSpikeProtectionEnabled
	common.CostSpikeProtectionEnabled = false
	defer func() { common.CostSpikeProtectionEnabled = prev }()
	r := mountCostSpike(42)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/cs", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 when protection disabled", w.Code)
	}
}

func TestCostSpikeLimit_NoUser_Passes(t *testing.T) {
	prev := common.CostSpikeProtectionEnabled
	common.CostSpikeProtectionEnabled = true
	defer func() { common.CostSpikeProtectionEnabled = prev }()
	r := mountCostSpike(0) // no id set
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/cs", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 when user id absent", w.Code)
	}
}

func TestCostSpikeLimit_RedisDisabled_FailsOpen(t *testing.T) {
	prevEnabled := common.CostSpikeProtectionEnabled
	prevRedis := common.RedisEnabled
	common.CostSpikeProtectionEnabled = true
	common.RedisEnabled = false
	defer func() {
		common.CostSpikeProtectionEnabled = prevEnabled
		common.RedisEnabled = prevRedis
	}()
	r := mountCostSpike(42)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/cs", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (fail open) when Redis unavailable", w.Code)
	}
}

// ============================ rate-limit =================================

func TestMemoryRateLimiterKeyed_AbortsOverLimit(t *testing.T) {
	inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
	// First request under limit passes.
	c1, w1 := newTestContext(http.MethodGet, "/x", "", "")
	memoryRateLimiterKeyed(c1, 1, 3600, "UT1", "identA")
	if c1.IsAborted() {
		t.Fatalf("first request must not be aborted")
	}
	// Second request on the same bucket exceeds and aborts 429. Read the
	// status from the gin writer (c.Status doesn't flush to the httptest
	// recorder's Code without an explicit WriteHeaderNow).
	c2, w2 := newTestContext(http.MethodGet, "/x", "", "")
	memoryRateLimiterKeyed(c2, 1, 3600, "UT1", "identA")
	if !c2.IsAborted() || c2.Writer.Status() != http.StatusTooManyRequests {
		t.Errorf("second request aborted=%v status=%d, want abort 429", c2.IsAborted(), c2.Writer.Status())
	}
	_ = w1
	if w2.Header().Get("X-RateLimit-Limit") == "" {
		t.Errorf("rate limit headers not set on 429")
	}
}

func TestRateLimitFactory_MemorySelected(t *testing.T) {
	prev := common.RedisEnabled
	common.RedisEnabled = false
	defer func() { common.RedisEnabled = prev }()
	f := rateLimitFactory(1, 3600, "UTFAC")

	run := func(ip string) int {
		r := gin.New()
		r.Use(gin.Recovery())
		r.GET("/f", f, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
		req := httptest.NewRequest(http.MethodGet, "/f", nil)
		req.RemoteAddr = ip + ":1111"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	if code := run("203.0.113.10"); code != http.StatusOK {
		t.Errorf("first request status = %d, want 200", code)
	}
	if code := run("203.0.113.10"); code != http.StatusTooManyRequests {
		t.Errorf("second request status = %d, want 429", code)
	}
}

func TestRateLimitConstructors_DisabledPassThrough(t *testing.T) {
	prevWeb := common.GlobalWebRateLimitEnable
	prevApi := common.GlobalApiRateLimitEnable
	prevCrit := common.CriticalRateLimitEnable
	common.GlobalWebRateLimitEnable = false
	common.GlobalApiRateLimitEnable = false
	common.CriticalRateLimitEnable = false
	defer func() {
		common.GlobalWebRateLimitEnable = prevWeb
		common.GlobalApiRateLimitEnable = prevApi
		common.CriticalRateLimitEnable = prevCrit
	}()

	for name, mw := range map[string]func(c *gin.Context){
		"web":  GlobalWebRateLimit(),
		"api":  GlobalAPIRateLimit(),
		"crit": CriticalRateLimit(),
	} {
		r := gin.New()
		r.GET("/p", mw, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/p", nil))
		if w.Code != http.StatusOK {
			t.Errorf("%s disabled limiter status = %d, want 200 pass-through", name, w.Code)
		}
	}
}

func TestRateLimitConstructors_EnabledSingleRequest(t *testing.T) {
	prev := common.RedisEnabled
	common.RedisEnabled = false
	defer func() { common.RedisEnabled = prev }()

	ctors := map[string]func() func(c *gin.Context){
		"download":   DownloadRateLimit,
		"upload":     UploadRateLimit,
		"redemption": RedemptionRateLimit,
		"topup":      TopupRateLimit,
		"bootstrap":  BootstrapRateLimit,
	}
	i := 0
	for name, ctor := range ctors {
		i++
		mw := ctor()
		r := gin.New()
		r.GET("/c", mw, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
		req := httptest.NewRequest(http.MethodGet, "/c", nil)
		req.RemoteAddr = "198.51.100." + itoa(i) + ":2222"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s first request status = %d, want 200", name, w.Code)
		}
	}
}

func TestInternalApiRateLimitKey(t *testing.T) {
	c1, _ := newTestContext(http.MethodGet, "/x", "", "")
	c1.Set("internal_api_key_id", 7)
	if got := internalApiRateLimitKey(c1); got != "ik:7" {
		t.Errorf("key = %q, want ik:7", got)
	}
	c2, _ := newTestContext(http.MethodGet, "/x", "", "")
	c2.Request.RemoteAddr = "192.0.2.55:3333"
	if got := internalApiRateLimitKey(c2); got == "" || got[:3] != "ip:" {
		t.Errorf("key = %q, want ip:<addr> fallback", got)
	}
}

func TestInternalApiRateLimit_DisabledPassThrough(t *testing.T) {
	prev := common.InternalApiRateLimitEnable
	common.InternalApiRateLimitEnable = false
	defer func() { common.InternalApiRateLimitEnable = prev }()
	mw := InternalApiRateLimit(1, 60, "UTI")
	r := gin.New()
	r.GET("/i", mw, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/i", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 when internal api rate limit disabled", w.Code)
	}
}

func TestInternalApiRateLimit_EnabledKeyed(t *testing.T) {
	prevEnable := common.InternalApiRateLimitEnable
	prevRedis := common.RedisEnabled
	common.InternalApiRateLimitEnable = true
	common.RedisEnabled = false
	defer func() {
		common.InternalApiRateLimitEnable = prevEnable
		common.RedisEnabled = prevRedis
	}()
	mw := InternalApiRateLimit(1, 3600, "UTIK")
	run := func() int {
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("internal_api_key_id", 99); c.Next() })
		r.GET("/i", mw, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/i", nil))
		return w.Code
	}
	if code := run(); code != http.StatusOK {
		t.Errorf("first keyed request = %d, want 200", code)
	}
	if code := run(); code != http.StatusTooManyRequests {
		t.Errorf("second keyed request = %d, want 429 (same key bucket)", code)
	}
}

// ============================ model-rate-limit ===========================

func TestModelRequestRateLimit_Disabled_Passes(t *testing.T) {
	prev := setting.ModelRequestRateLimitEnabled
	setting.ModelRequestRateLimitEnabled = false
	defer func() { setting.ModelRequestRateLimitEnabled = prev }()
	r := gin.New()
	r.GET("/m", ModelRequestRateLimit(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/m", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 when model rate limit disabled", w.Code)
	}
}

func TestModelRequestRateLimit_MemoryHandler_TotalLimit(t *testing.T) {
	prevEnabled := setting.ModelRequestRateLimitEnabled
	prevCount := setting.ModelRequestRateLimitCount
	prevSuccess := setting.ModelRequestRateLimitSuccessCount
	prevRedis := common.RedisEnabled
	setting.ModelRequestRateLimitEnabled = true
	setting.ModelRequestRateLimitCount = 1 // total budget = 1
	setting.ModelRequestRateLimitSuccessCount = 1000
	common.RedisEnabled = false
	defer func() {
		setting.ModelRequestRateLimitEnabled = prevEnabled
		setting.ModelRequestRateLimitCount = prevCount
		setting.ModelRequestRateLimitSuccessCount = prevSuccess
		common.RedisEnabled = prevRedis
	}()

	const uid = 987654
	run := func() int {
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("id", uid); c.Next() })
		r.GET("/m", ModelRequestRateLimit(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/m", nil))
		return w.Code
	}
	if code := run(); code != http.StatusOK {
		t.Errorf("first request = %d, want 200", code)
	}
	if code := run(); code != http.StatusTooManyRequests {
		t.Errorf("second request = %d, want 429 (total budget exhausted)", code)
	}
}

// ============================ admin_jwt_auth =============================

func TestAdminJWTAuth_NoHeader_FallsToSessionAuth_401(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())
	store := cookie.NewStore([]byte("ajwt-secret"))
	r.Use(sessions.Sessions("session", store))
	r.GET("/a", AdminJWTAuth(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	// No Authorization header + no session → authHelper rejects with 401.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/a", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for anonymous admin jwt fallback", w.Code)
	}
}

func TestRootJWTAuth_NoHeader_FallsToSessionAuth_401(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())
	store := cookie.NewStore([]byte("rjwt-secret"))
	r.Use(sessions.Sessions("session", store))
	r.GET("/root", RootJWTAuth(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/root", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for anonymous root jwt fallback", w.Code)
	}
}

func TestAdminJWTAuth_HeaderPresent_OIDCDisabled_FallsToSession(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	r := gin.New()
	r.Use(gin.Recovery())
	store := cookie.NewStore([]byte("ajwt2-secret"))
	r.Use(sessions.Sessions("session", store))
	r.GET("/a", AdminJWTAuth(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	// Header present but oidc disabled → authHelper path. The bogus bearer is
	// not a valid identity-session token nor an access token, so authHelper
	// aborts (never reaching the terminal handler). authHelper returns HTTP 200
	// with success:false for an invalid access token, so assert on the abort
	// (handler not reached) rather than the status code.
	req := httptest.NewRequest(http.MethodGet, "/a", nil)
	req.Header.Set("Authorization", "Bearer whatever")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if strings.Contains(w.Body.String(), `"ok":true`) {
		t.Errorf("terminal handler was reached; middleware must reject invalid credential; body=%s", w.Body.String())
	}
}

func TestValidateJWT_Errors(t *testing.T) {
	// No Authorization header.
	c1, _ := newTestContext(http.MethodGet, "/x", "", "")
	if _, err := validateJWT(c1); err == nil {
		t.Errorf("expected error for missing Authorization header")
	}
	// Header present but not a Bearer token.
	c2, _ := newTestContext(http.MethodGet, "/x", "", "")
	c2.Request.Header.Set("Authorization", "Basic abc")
	if _, err := validateJWT(c2); err == nil {
		t.Errorf("expected error for non-Bearer Authorization")
	}
}

func TestHasRole(t *testing.T) {
	roles := []string{"user", "admin"}
	if !hasRole(roles, "admin") {
		t.Errorf("hasRole should find admin")
	}
	if hasRole(roles, "root") {
		t.Errorf("hasRole should not find root")
	}
	if hasRole(nil, "admin") {
		t.Errorf("hasRole(nil) should be false")
	}
}

// ============================ auth remaining =============================

func TestTryUserAuth_SetsIDFromSession(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())
	store := cookie.NewStore([]byte("try-secret"))
	r.Use(sessions.Sessions("session", store))
	r.Use(func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("id", 314)
		_ = s.Save()
		c.Next()
	})
	r.GET("/t", TryUserAuth(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"id": c.GetInt("id")})
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/t", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestTryUserAuth_NoSession_StillNext(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())
	store := cookie.NewStore([]byte("try2-secret"))
	r.Use(sessions.Sessions("session", store))
	r.GET("/t", TryUserAuth(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/t", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (TryUserAuth never blocks)", w.Code)
	}
}

func TestAdminAuth_And_RootAuth_Anonymous_401(t *testing.T) {
	for name, mw := range map[string]func() func(c *gin.Context){"admin": AdminAuth, "root": RootAuth} {
		r := gin.New()
		r.Use(gin.Recovery())
		store := cookie.NewStore([]byte(name + "-secret"))
		r.Use(sessions.Sessions("session", store))
		r.GET("/x", mw(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s anonymous status = %d, want 401", name, w.Code)
		}
	}
}

func TestWssAuth_NoOp(t *testing.T) {
	c, _ := newTestContext(http.MethodGet, "/x", "", "")
	WssAuth(c) // must not panic or abort
	if c.IsAborted() {
		t.Errorf("WssAuth must be a no-op, not abort")
	}
}

func TestPlaygroundAuth_NoSession_401(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())
	store := cookie.NewStore([]byte("pg-secret"))
	r.Use(sessions.Sessions("session", store))
	r.POST("/pg", PlaygroundAuth(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	// No Authorization header + no session id → 401.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/pg", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for playground without session", w.Code)
	}
}

// itoa is a tiny helper to avoid importing strconv just for one call.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}
