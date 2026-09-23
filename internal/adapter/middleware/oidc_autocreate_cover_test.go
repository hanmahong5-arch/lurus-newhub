package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

func TestMapOIDCUserToLurus_AutoCreateTenantAndUser(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	_ = os.Setenv("OIDC_AUTO_CREATE_TENANT", "true")
	_ = os.Setenv("OIDC_AUTO_CREATE_USER", "true")
	defer func() {
		_ = os.Unsetenv("OIDC_AUTO_CREATE_TENANT")
		_ = os.Unsetenv("OIDC_AUTO_CREATE_USER")
	}()

	claims := &OIDCClaims{
		OrgID:             "brand-new-org",
		OrgDomain:         "brandnew.test",
		ResourceOwnerName: "Brand New Org",
		Email:             "newbie@brandnew.test",
		Name:              "Newbie",
		PreferredUsername: "newbie",
	}
	claims.Subject = "brand-new-sub"

	tid, uid, err := mapOIDCUserToLurus(claims)
	if err != nil {
		t.Fatalf("auto-create path returned error: %v", err)
	}
	if tid == "" || uid == 0 {
		t.Errorf("auto-create returned empty ids: tenant=%q user=%d", tid, uid)
	}
}

func TestMapOIDCUserToLurus_AutoCreateUserOnly(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	// Tenant already exists; only the user mapping is auto-created.
	tn := &entity.Tenant{Id: "existing-t", Slug: "existing-t", Name: "n", Status: entity.TenantStatusEnabled, IDPOrgID: "existing-org", PlanType: entity.TenantPlanFree, MaxUsers: 10, MaxQuota: 1000, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := repo.DB.Create(tn).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	_ = os.Setenv("OIDC_AUTO_CREATE_USER", "true")
	defer func() { _ = os.Unsetenv("OIDC_AUTO_CREATE_USER") }()

	claims := &OIDCClaims{OrgID: "existing-org", Email: "fresh@existing.test", Name: "Fresh", PreferredUsername: "fresh"}
	claims.Subject = "fresh-sub"
	tid, uid, err := mapOIDCUserToLurus(claims)
	if err != nil {
		t.Fatalf("auto-create-user path error: %v", err)
	}
	if tid != "existing-t" || uid == 0 {
		t.Errorf("got (%s,%d), want existing-t + a new user id", tid, uid)
	}
}

// -------------------- lookupReleaseProductID --------------------

func TestLookupReleaseProductID(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	rel := &entity.Release{Id: 55, ProductId: "lurus-switch", Version: "1.0.0", Title: "v1", IsPublished: true}
	if err := repo.DB.Create(rel).Error; err != nil {
		t.Fatalf("create release: %v", err)
	}
	pid, found := lookupReleaseProductID(context.Background(), 55)
	if !found || pid != "lurus-switch" {
		t.Errorf("lookupReleaseProductID(55) = (%q,%v), want (lurus-switch,true)", pid, found)
	}
	// Missing release → not found.
	if _, found := lookupReleaseProductID(context.Background(), 9999); found {
		t.Errorf("lookupReleaseProductID(9999) found=true, want false")
	}
}

// -------------------- cost_spike normal pass-through --------------------

func TestCostSpikeLimit_UnderLimit_Passes(t *testing.T) {
	_, dbCleanup := setupCoverDB(t)
	defer dbCleanup()
	redisCleanup := withRedis(t)
	defer redisCleanup()

	user := &repo.User{Username: "calm", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Email: "c@local", TenantId: "default"}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	prevEnabled := common.CostSpikeProtectionEnabled
	prevLimit := common.CostSpikeHardLimitPer5Min
	common.CostSpikeProtectionEnabled = true
	common.CostSpikeHardLimitPer5Min = 1_000_000 // window (empty=0) stays well under
	defer func() {
		common.CostSpikeProtectionEnabled = prevEnabled
		common.CostSpikeHardLimitPer5Min = prevLimit
	}()

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("id", user.Id); c.Next() })
	r.GET("/relay", CostSpikeLimit(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/relay", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 when usage under limit", w.Code)
	}
}

// -------------------- resolveOIDCClaims all keys --------------------

func TestResolveOIDCClaims_AllCustomKeys(t *testing.T) {
	prev := oidcClaimKeys
	defer func() { oidcClaimKeys = prev }()
	oidcClaimKeys = claimKeySet{
		OrgID:             "c_org",
		OrgDomain:         "c_domain",
		ResourceOwnerID:   "c_ro_id",
		ResourceOwnerName: "c_ro_name",
		Roles:             "c_roles",
	}
	var c OIDCClaims
	resolveOIDCClaims(&c, map[string]interface{}{
		"c_org":     "o1",
		"c_domain":  "d1",
		"c_ro_id":   "ro1",
		"c_ro_name": "roname",
		"c_roles":   map[string]interface{}{"admin": map[string]interface{}{}},
	})
	if c.OrgID != "o1" || c.OrgDomain != "d1" || c.ResourceOwnerID != "ro1" || c.ResourceOwnerName != "roname" {
		t.Errorf("resolved claims mismatch: %+v", c)
	}
	if c.Roles == nil {
		t.Errorf("roles not resolved from custom key")
	}
}

// -------------------- NewJWKSManager direct --------------------

func TestNewJWKSManager_Direct(t *testing.T) {
	_, pub := generateTestRSAKeyPair(t)
	jwks := JWKSet{Keys: []JWK{rsaPublicKeyToJWK(pub, "direct-kid")}}
	srv := createTestJWKSServer(t, jwks)
	defer srv.Close()

	// With a cancelled-at-cleanup context: NewJWKSManager's Background
	// context left the refresh goroutine running for the rest of the package
	// run, where it raced later tests' writes to jwksRefreshInterval.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr := NewJWKSManagerWithContext(ctx, srv.URL)
	if mgr == nil {
		t.Fatalf("NewJWKSManagerWithContext returned nil")
	}
	if key, err := mgr.getKeyWithRefresh("direct-kid"); err != nil || key == nil {
		t.Errorf("getKeyWithRefresh err=%v key=%v, want fetched key", err, key)
	}
}
