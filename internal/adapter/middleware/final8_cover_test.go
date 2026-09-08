package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// timeMinus returns a timestamp d in the past.
func timeMinus(d time.Duration) time.Time { return time.Now().Add(-d) }

// validUserInfo rejects an unrecognized role even for a non-empty username.
func TestValidUserInfo(t *testing.T) {
	if validUserInfo("someone", 99999) {
		t.Errorf("validUserInfo with invalid role must be false")
	}
	if validUserInfo("   ", common.RoleCommonUser) {
		t.Errorf("validUserInfo with blank username must be false")
	}
	if !validUserInfo("someone", common.RoleCommonUser) {
		t.Errorf("validUserInfo with valid username+role must be true")
	}
}

// TokenAuth propagates a token's IdentityAccountID into the gin context so the
// platform billing bridge can attribute usage.
func TestTokenAuth_PropagatesIdentityAccountID(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	user := &repo.User{Username: "billed", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Email: "b@local", TenantId: "default"}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	key := common.GetRandomString(48)
	tok := &repo.Token{
		UserId: user.Id, TenantId: "default", Key: key, Status: common.TokenStatusEnabled, Name: "billed",
		ExpiredTime: -1, UnlimitedQuota: true, IdentityAccountID: 7788,
		CreatedTime: common.GetTimestamp(), AccessedTime: common.GetTimestamp(),
	}
	if err := repo.DB.Create(tok).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	var acct int64
	r := gin.New()
	r.Use(gin.Recovery())
	r.POST("/v1/chat/completions", TokenAuth(), func(c *gin.Context) {
		if v, ok := c.Get("identity_account_id"); ok {
			acct, _ = v.(int64)
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer sk-"+key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if acct != 7788 {
		t.Errorf("identity_account_id = %d, want 7788", acct)
	}
}

// EntitlementCheck: a stale cache entry (older than the TTL) forces a refresh;
// with no identity URL the refresh yields the free plan → allowed.
func TestEntitlementCheck_StaleCache_Refreshes(t *testing.T) {
	const acct = int64(880123)
	key := entitlementCacheKey{accountID: acct, product: ratio_setting.DefaultSourceProduct}
	entitlementCache.Store(key, entitlementEntry{allowed: false, checkedAt: timeMinus(entitlementCacheTTL + 1)})
	defer entitlementCache.Delete(key)

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("identity_account_id", acct); c.Next() })
	r.GET("/e", EntitlementCheck(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/e", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 after stale-cache refresh to free plan", w.Code)
	}
}

// getModelRequest: audio transcriptions with an explicit model keeps it (rather
// than defaulting to whisper-1).
func TestGetModelRequest_AudioTranscriptions_ExplicitModel(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/v1/audio/transcriptions", `{"model":"whisper-large-v3"}`, "application/json")
	mr, _, err := getModelRequest(c)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if mr.Model != "whisper-large-v3" {
		t.Errorf("model = %q, want whisper-large-v3 (explicit model preserved)", mr.Model)
	}
}
