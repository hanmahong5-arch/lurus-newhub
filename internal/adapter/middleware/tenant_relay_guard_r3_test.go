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
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Round-3 tenant-enforcement regression suite. Three already-confirmed relay
// hot-path defects, each exercised in BOTH directions (legitimate traffic keeps
// working; the abusive/leaky case is now rejected):
//
//	#1  sk-<key>-<channelId> override must not relay through a channel owned by
//	    another tenant (upstream-key exfiltration). Same-tenant + root allowed.
//	#2  TokenAuth must reject a token whose owning tenant was disabled/suspended;
//	    an enabled tenant passes and the "default" system tenant is never locked.
//	#3  PoolBalanceCheck must actually fire on the token path (it was dead code
//	    because TokenAuth never injected tenant_context): exhausted pool → 402,
//	    sufficient/absent pool → pass.

// --- shared seeding helpers ------------------------------------------------

func seedChannelR3(t *testing.T, db *gorm.DB, id int, tenantId string) {
	t.Helper()
	ch := &repo.Channel{
		Id: id, Name: "ch", TenantId: tenantId, Key: "k",
		Status: common.ChannelStatusEnabled, Models: "gpt-4o", Group: "default",
	}
	if err := db.Create(ch).Error; err != nil {
		t.Fatalf("create channel %d: %v", id, err)
	}
}

func seedTenantR3(t *testing.T, db *gorm.DB, id string, status int) {
	t.Helper()
	tn := &entity.Tenant{Id: id, IDPOrgID: "org-" + id, Slug: id, Name: id, Status: status}
	if err := db.Create(tn).Error; err != nil {
		t.Fatalf("create tenant %s: %v", id, err)
	}
}

func seedPoolR3(t *testing.T, db *gorm.DB, tenantId string, maxBal, curBal int64) {
	t.Helper()
	p := &entity.TenantCreditPool{
		TenantID:          tenantId,
		CreatedByUserID:   1,
		CurrentBalance:    curBal,
		MaxBalance:        maxBal,
		ResetPeriod:       "monthly",
		LastResetAt:       time.Now(),
		AlertThresholdPct: 80,
	}
	if err := db.Create(p).Error; err != nil {
		t.Fatalf("create pool %s: %v", tenantId, err)
	}
}

// seedRelayUserToken creates a User + Token in the given tenant and returns the
// raw token key (no "sk-"/"-channel" suffix, so TokenAuth resolves it directly).
func seedRelayUserToken(t *testing.T, db *gorm.DB, tenantId string, userStatus, tokenStatus int) string {
	t.Helper()
	sfx := common.GetRandomString(6)
	user := &repo.User{
		Username:    "relay-" + sfx,
		DisplayName: "Relay " + sfx,
		Role:        common.RoleCommonUser,
		Status:      userStatus,
		Email:       "relay-" + sfx + "@local",
		TenantId:    tenantId,
		Quota:       1_000_000,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	key := common.GetRandomString(48)
	tok := &repo.Token{
		UserId:         user.Id,
		TenantId:       tenantId,
		Key:            key,
		Status:         tokenStatus,
		Name:           "relay-" + sfx,
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}
	if err := db.Create(tok).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}
	return key
}

// mountTokenAuthRelay wires the real TokenAuth() (plus any extra middleware such
// as PoolBalanceCheck) around a 200 terminal handler — anything other than 200
// means a middleware aborted.
func mountTokenAuthRelay(extra ...gin.HandlerFunc) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	chain := []gin.HandlerFunc{TokenAuth()}
	chain = append(chain, extra...)
	chain = append(chain, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"success": true}) })
	r.POST("/v1/chat/completions", chain...)
	return r
}

func probeRelay(r *gin.Engine, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// --- #1 specific-channel override tenant ownership -------------------------

func TestDistribute_Override_SameTenant_Allowed(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()
	seedChannelR3(t, db, 50, "tenant-a")

	r := mountDistribute(func(c *gin.Context) {
		c.Set("tenant_context", &TenantContext{TenantID: "tenant-a"})
		common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "50")
	})
	w := doDistribute(r, `{"model":"gpt-4o"}`)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for same-tenant channel override; body=%s", w.Code, w.Body.String())
	}
}

func TestDistribute_Override_CrossTenant_NonRoot_Denied(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()
	seedChannelR3(t, db, 60, "tenant-b")

	r := mountDistribute(func(c *gin.Context) {
		c.Set("tenant_context", &TenantContext{TenantID: "tenant-a"})
		common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "60")
	})
	w := doDistribute(r, `{"model":"gpt-4o"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for cross-tenant channel override; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "其他租户") {
		t.Errorf("expected cross-tenant rejection message; body=%s", w.Body.String())
	}
}

// E1: when the caller's tenant_context cannot be resolved at all (no
// "tenant_context" key set — e.g. a code path that reaches Distribute without
// going through TokenAuth), a non-root channel pin must fail CLOSED (403),
// not silently skip the ownership check and let the pin through.
func TestDistribute_Override_UnresolvableTenant_NonRoot_Denied(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()
	seedChannelR3(t, db, 62, "tenant-b")

	r := mountDistribute(func(c *gin.Context) {
		// Deliberately do NOT set "tenant_context" — simulates GetTenantContext
		// erroring out for a non-root caller attempting a channel pin.
		common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "62")
	})
	w := doDistribute(r, `{"model":"gpt-4o"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 when tenant context can't be resolved for a non-root channel pin; body=%s", w.Code, w.Body.String())
	}
}

func TestDistribute_Override_CrossTenant_Root_Allowed(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()
	seedChannelR3(t, db, 61, "tenant-b")

	r := mountDistribute(func(c *gin.Context) {
		c.Set("tenant_context", &TenantContext{TenantID: "tenant-a"})
		common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "61")
		// Root override flag (raw key mirrors the constant's string value) — a
		// root operator keeps the cross-tenant channel override.
		c.Set("specific_channel_root_override", true)
	})
	w := doDistribute(r, `{"model":"gpt-4o"}`)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for root cross-tenant override; body=%s", w.Code, w.Body.String())
	}
}

// --- #2 TokenAuth tenant-status enforcement --------------------------------

func TestTokenAuth_EnabledTenant_Passes(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()
	seedTenantR3(t, db, "t-live", entity.TenantStatusEnabled)
	key := seedRelayUserToken(t, db, "t-live", common.UserStatusEnabled, common.TokenStatusEnabled)

	w := probeRelay(mountTokenAuthRelay(), key)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for enabled-tenant token; body=%s", w.Code, w.Body.String())
	}
}

func TestTokenAuth_DisabledTenant_Rejected(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()
	seedTenantR3(t, db, "t-dead", entity.TenantStatusDisabled)
	key := seedRelayUserToken(t, db, "t-dead", common.UserStatusEnabled, common.TokenStatusEnabled)

	w := probeRelay(mountTokenAuthRelay(), key)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for disabled-tenant token; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "租户") {
		t.Errorf("expected tenant-disabled message; body=%s", w.Body.String())
	}
}

func TestTokenAuth_SuspendedTenant_Rejected(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()
	seedTenantR3(t, db, "t-susp", entity.TenantStatusSuspended)
	key := seedRelayUserToken(t, db, "t-susp", common.UserStatusEnabled, common.TokenStatusEnabled)

	w := probeRelay(mountTokenAuthRelay(), key)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for suspended-tenant token; body=%s", w.Code, w.Body.String())
	}
}

func TestTokenAuth_DefaultTenant_NotLocked(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()
	// Even a (pathological) disabled "default" tenant row must never lock the
	// bootstrap/system tenant out — the carve-out mirrors authHelper.
	seedTenantR3(t, db, "default", entity.TenantStatusDisabled)
	key := seedRelayUserToken(t, db, "default", common.UserStatusEnabled, common.TokenStatusEnabled)

	w := probeRelay(mountTokenAuthRelay(), key)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 — default tenant must never be locked; body=%s", w.Code, w.Body.String())
	}
}

// --- #3 PoolBalanceCheck now fires on the token path -----------------------

func TestTokenAuth_PoolExhausted_Blocks(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()
	seedTenantR3(t, db, "t-pool-x", entity.TenantStatusEnabled)
	seedPoolR3(t, db, "t-pool-x", 1000, 0) // finite ceiling + zero balance = exhausted
	key := seedRelayUserToken(t, db, "t-pool-x", common.UserStatusEnabled, common.TokenStatusEnabled)

	w := probeRelay(mountTokenAuthRelay(PoolBalanceCheck()), key)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402 for exhausted pool on token path; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "pool_exhausted") {
		t.Errorf("expected pool_exhausted body; got %s", w.Body.String())
	}
}

func TestTokenAuth_PoolSufficient_Passes(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()
	seedTenantR3(t, db, "t-pool-ok", entity.TenantStatusEnabled)
	seedPoolR3(t, db, "t-pool-ok", 1000, 500)
	key := seedRelayUserToken(t, db, "t-pool-ok", common.UserStatusEnabled, common.TokenStatusEnabled)

	w := probeRelay(mountTokenAuthRelay(PoolBalanceCheck()), key)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for sufficient pool; body=%s", w.Code, w.Body.String())
	}
}

func TestTokenAuth_NoPool_Passes(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()
	seedTenantR3(t, db, "t-nopool", entity.TenantStatusEnabled)
	key := seedRelayUserToken(t, db, "t-nopool", common.UserStatusEnabled, common.TokenStatusEnabled)

	w := probeRelay(mountTokenAuthRelay(PoolBalanceCheck()), key)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 when tenant has no pool row; body=%s", w.Code, w.Body.String())
	}
}
