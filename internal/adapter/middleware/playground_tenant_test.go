package middleware

// playground_tenant_test.go — L1: PlaygroundAuth must resolve and enforce
// the SAME tenant-isolation gates the /v1 (bearer) relay path enforces —
// before this fix it never injected tenant context at all, so the ordinary
// weighted channel-selection path (already locked for bearer tokens by
// tenant_relay_selection_test.go) ran tenant-BLIND for every playground
// caller: a tenant-b user hitting the playground could land on a
// tenant-a-owned channel purely by weight, and any downstream code that
// reads GetTenantContext saw nothing at all.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

// mountPlaygroundChain builds an engine with a cookie session that seeds the
// given user id, then runs the REAL PlaygroundAuth() -> Distribute() chain
// (not a hand-copy of it) ending in a terminal handler that echoes back the
// selected channel id and the tenant context the chain resolved, so tests
// can assert on both in one round trip.
func mountPlaygroundChain(userID int) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	store := cookie.NewStore([]byte("pg-tenant-secret"))
	r.Use(sessions.Sessions("session", store))
	r.Use(func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("id", userID)
		_ = s.Save()
		c.Next()
	})
	r.POST("/pg/chat/completions", PlaygroundAuth(), Distribute(), func(c *gin.Context) {
		tenantID := ""
		if tc, err := GetTenantContext(c); err == nil && tc != nil {
			tenantID = tc.TenantID
		}
		c.JSON(http.StatusOK, gin.H{
			"channel_id": common.GetContextKeyInt(c, constant.ContextKeyChannelId),
			"tenant_id":  tenantID,
		})
	})
	return r
}

func doPlayground(r *gin.Engine, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/pg/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestPlaygroundAuth_ForeignTenantChannelNeverServed drives the real
// PlaygroundAuth()+Distribute() chain 50 times for a tenant-b session user
// against a tenant-a channel seeded at 1000x the weight of a platform-shared
// channel: only an injected tenant context can explain the shared channel
// winning every single time.
func TestPlaygroundAuth_ForeignTenantChannelNeverServed(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	prevCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = prevCache })

	seedTenantRelayChannel(t, db, 9801, "tenant-a", 1000) // heavily favoured, owned by tenant-a
	seedTenantRelayChannel(t, db, 9802, "default", 1)     // platform-shared
	repo.InitChannelCache()

	user := &repo.User{
		Username: "pg-user-" + common.GetRandomString(6), Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: "pg@local", TenantId: "tenant-b", Group: "default",
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	tok := &repo.Token{
		UserId: user.Id, TenantId: "tenant-b", Key: common.GetRandomString(48),
		Status: common.TokenStatusEnabled, Name: "pg-default",
	}
	if err := db.Create(tok).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	for i := 0; i < 50; i++ {
		r := mountPlaygroundChain(user.Id)
		w := doPlayground(r, `{"model":"gpt-4o"}`)
		if w.Code != http.StatusOK {
			t.Fatalf("attempt %d: status = %d, body=%s", i, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"channel_id":9802`) {
			t.Fatalf("attempt %d: tenant-b playground caller was served a channel other than the platform-shared one: %s", i, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"tenant_id":"tenant-b"`) {
			t.Fatalf("attempt %d: terminal handler tenant context = %s, want tenant-b", i, w.Body.String())
		}
	}
}

// TestPlaygroundAuth_DisabledUser_Rejected mirrors
// tenant_relay_guard_r3_test.go's TokenAuth disabled-user coverage: a
// session user whose account row is disabled must be rejected before the
// playground relays anything on their behalf.
func TestPlaygroundAuth_DisabledUser_Rejected(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	user := &repo.User{
		Username: "pg-disabled-" + common.GetRandomString(6), Role: common.RoleCommonUser,
		Status: common.UserStatusDisabled, Email: "pg-dis@local", TenantId: "default", Group: "default",
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	tok := &repo.Token{
		UserId: user.Id, TenantId: "default", Key: common.GetRandomString(48),
		Status: common.TokenStatusEnabled, Name: "pg-default",
	}
	if err := db.Create(tok).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	r := mountPlaygroundChain(user.Id)
	w := doPlayground(r, `{"model":"gpt-4o"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for disabled owning user; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "封禁") {
		t.Errorf("expected disabled-user message; body=%s", w.Body.String())
	}
}

// TestPlaygroundAuth_DisabledTenant_Rejected mirrors
// TestTokenAuth_DisabledTenant_Rejected (tenant_relay_guard_r3_test.go): a
// playground session whose owning user belongs to a disabled tenant must be
// locked out, same as the bearer-token relay path.
func TestPlaygroundAuth_DisabledTenant_Rejected(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	if err := db.Create(&entity.Tenant{Id: "t-pg-dead", IDPOrgID: "org-t-pg-dead", Slug: "t-pg-dead", Name: "t-pg-dead", Status: entity.TenantStatusDisabled}).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	user := &repo.User{
		Username: "pg-deadtenant-" + common.GetRandomString(6), Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: "pg-dead@local", TenantId: "t-pg-dead", Group: "default",
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	tok := &repo.Token{
		UserId: user.Id, TenantId: "t-pg-dead", Key: common.GetRandomString(48),
		Status: common.TokenStatusEnabled, Name: "pg-default",
	}
	if err := db.Create(tok).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	r := mountPlaygroundChain(user.Id)
	w := doPlayground(r, `{"model":"gpt-4o"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for disabled tenant; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "TENANT_DISABLED") {
		t.Errorf("expected TENANT_DISABLED error_code; body=%s", w.Body.String())
	}
}

// TestPlaygroundAuth_TenantLookupError_FailsOpen: a token row whose owning
// user id has no corresponding user row (GetUserCache -> GetUserById fails)
// must still relay — the tenant resolution fails OPEN, same as authHelper's
// session path and TokenAuth's token-path resolution, rather than turning a
// transient/missing lookup into a hard rejection of every playground call.
func TestPlaygroundAuth_TenantLookupError_FailsOpen(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	prevCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = prevCache })

	seedTenantRelayChannel(t, db, 9803, "default", 1)
	repo.InitChannelCache()

	danglingUserID := 424242 // no repo.User row created for this id
	tok := &repo.Token{
		UserId: danglingUserID, TenantId: "default", Key: common.GetRandomString(48),
		Status: common.TokenStatusEnabled, Name: "pg-dangling",
	}
	if err := db.Create(tok).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	r := mountPlaygroundChain(danglingUserID)
	w := doPlayground(r, `{"model":"gpt-4o"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (tenant lookup failure must fail OPEN, not reject); body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"tenant_id":"default"`) {
		t.Errorf("expected fallback tenant_id=default when lookup fails; body=%s", w.Body.String())
	}
}
