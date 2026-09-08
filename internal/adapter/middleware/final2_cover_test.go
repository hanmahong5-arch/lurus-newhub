package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// -------------------- rate-limit enabled constructors --------------------

func TestRateLimitConstructors_EnabledFactory(t *testing.T) {
	prevRedis := common.RedisEnabled
	prevWeb := common.GlobalWebRateLimitEnable
	prevApi := common.GlobalApiRateLimitEnable
	prevCrit := common.CriticalRateLimitEnable
	prevWebNum := common.GlobalWebRateLimitNum
	prevApiNum := common.GlobalApiRateLimitNum
	prevCritNum := common.CriticalRateLimitNum
	common.RedisEnabled = false
	common.GlobalWebRateLimitEnable = true
	common.GlobalApiRateLimitEnable = true
	common.CriticalRateLimitEnable = true
	common.GlobalWebRateLimitNum = 100
	common.GlobalApiRateLimitNum = 100
	common.CriticalRateLimitNum = 100
	defer func() {
		common.RedisEnabled = prevRedis
		common.GlobalWebRateLimitEnable = prevWeb
		common.GlobalApiRateLimitEnable = prevApi
		common.CriticalRateLimitEnable = prevCrit
		common.GlobalWebRateLimitNum = prevWebNum
		common.GlobalApiRateLimitNum = prevApiNum
		common.CriticalRateLimitNum = prevCritNum
	}()

	i := 0
	for name, mw := range map[string]func(c *gin.Context){
		"web":  GlobalWebRateLimit(),
		"api":  GlobalAPIRateLimit(),
		"crit": CriticalRateLimit(),
	} {
		i++
		r := gin.New()
		r.GET("/e", mw, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
		req := httptest.NewRequest(http.MethodGet, "/e", nil)
		req.RemoteAddr = "203.0.113." + itoa(100+i) + ":1"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s enabled-limiter first request = %d, want 200", name, w.Code)
		}
	}
}

func TestInternalApiRateLimit_RedisKeyed(t *testing.T) {
	cleanup := withRedis(t)
	defer cleanup()
	prev := common.InternalApiRateLimitEnable
	common.InternalApiRateLimitEnable = true
	defer func() { common.InternalApiRateLimitEnable = prev }()

	mw := InternalApiRateLimit(1, 3600, "RKEY")
	run := func() int {
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("internal_api_key_id", 321); c.Next() })
		r.GET("/i", mw, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/i", nil))
		return w.Code
	}
	if code := run(); code != http.StatusOK {
		t.Errorf("first redis-keyed = %d, want 200", code)
	}
	if code := run(); code != http.StatusTooManyRequests {
		t.Errorf("second redis-keyed = %d, want 429", code)
	}
}

// -------------------- SetupContextForSelectedChannel extra branches ------

func TestSetupContextForSelectedChannel_OrgAndMultiKey(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/v1/chat/completions", `{}`, "application/json")
	org := "org-abc"
	ch := &repo.Channel{
		Id:                 3,
		Name:               "multi",
		Type:               constant.ChannelTypeOpenAI,
		Key:                "k1\nk2",
		Status:             common.ChannelStatusEnabled,
		OpenAIOrganization: &org,
		ChannelInfo:        repo.ChannelInfo{IsMultiKey: true, MultiKeySize: 2},
	}
	if err := SetupContextForSelectedChannel(c, ch, "gpt-4o"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v := common.GetContextKeyString(c, constant.ContextKeyChannelOrganization); v != "org-abc" {
		t.Errorf("organization ctx = %q, want org-abc", v)
	}
	if !common.GetContextKeyBool(c, constant.ContextKeyChannelIsMultiKey) {
		t.Errorf("multi-key flag not set in context")
	}
}

func TestSetupContextForSelectedChannel_MokaAndCloudflare(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  int
	}{
		{"moka", constant.ChannelTypeMokaAI},
		{"cloudflare", constant.ChannelCloudflare},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestContext(http.MethodPost, "/v1/chat/completions", `{}`, "application/json")
			ch := &repo.Channel{Id: 1, Name: "c", Type: tc.typ, Key: "k", Status: common.ChannelStatusEnabled, Other: "v9"}
			if err := SetupContextForSelectedChannel(c, ch, "m"); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v, _ := c.Get("api_version"); v != "v9" {
				t.Errorf("api_version = %v, want v9", v)
			}
		})
	}
}

// -------------------- entitlement cache-miss (no identity URL) -----------

func TestEntitlementCheck_CacheMiss_AllowsFreePlan(t *testing.T) {
	// With no IdentityServiceURL configured, GetEntitlements returns the free
	// plan (quota_remaining defaults to -1 = unlimited) → allowed → cached.
	const acct = int64(880099)
	key := entitlementCacheKey{accountID: acct, product: ratio_setting.DefaultSourceProduct}
	entitlementCache.Delete(key)
	defer entitlementCache.Delete(key)

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("identity_account_id", acct); c.Next() })
	r.GET("/e", EntitlementCheck(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/e", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for free-plan entitlement", w.Code)
	}
	// Result should now be cached.
	if _, ok := entitlementCache.Load(key); !ok {
		t.Errorf("entitlement result not cached after miss")
	}
}

// -------------------- TokenAuth expired token --------------------

func TestTokenAuth_ExpiredToken_401(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	user := &repo.User{Username: "expu", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Email: "e@local", TenantId: "default"}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	key := common.GetRandomString(48)
	tok := &repo.Token{
		UserId: user.Id, TenantId: "default", Key: key, Status: common.TokenStatusEnabled, Name: "exp",
		ExpiredTime: common.GetTimestamp() - 3600, UnlimitedQuota: true, // already expired
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
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for expired token; body=%s", w.Code, w.Body.String())
	}
}

// -------------------- PlaygroundAuth disabled token --------------------

func TestPlaygroundAuth_DisabledToken_403(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	user := &repo.User{Username: "pgdis", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Email: "pgd@local", TenantId: "default", Quota: 1000}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	// The user's only token is disabled → PlaygroundAuth rejects with 403.
	tok := &repo.Token{UserId: user.Id, TenantId: "default", Key: common.GetRandomString(48), Status: common.TokenStatusDisabled, Name: "dis", ExpiredTime: -1, UnlimitedQuota: true, CreatedTime: common.GetTimestamp(), AccessedTime: common.GetTimestamp()}
	if err := repo.DB.Create(tok).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}
	r := mountWithSession(user.Id, http.MethodPost, "/pg", PlaygroundAuth())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/pg", nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for disabled playground token; body=%s", w.Code, w.Body.String())
	}
}
