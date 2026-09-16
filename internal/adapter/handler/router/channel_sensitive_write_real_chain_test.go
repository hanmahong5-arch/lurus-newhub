package router

// channel_sensitive_write_real_chain_test.go — R5 (repair-round ruling /
// B-7, A-4): the REAL-CHAIN oracle for L2's channel:sensitive_write gate.
// internal/adapter/handler/channel_sensitive_write_test.go drives the
// handler functions directly (v1 via a hand-built gin.Context, v2 via a
// test router that MIRRORS the v2 route table) — sound for the predicate
// and handler-ordering claims it makes, but neither proves the gate is
// actually reachable through the PRODUCTION route registration. This file
// closes that gap: one test per API family, built the same way
// root_seal_test.go and audit_routes_root_or_granted_test.go already do in
// this package — SetApiRouter/SetApiV2Router mount the real middleware
// chain (AdminAuth, and for v2 also TenantSlugGuard), and the session
// identity comes from a seeded repo.User row, not a hand-set context key.
//
// Mutation target: deleting the enforceChannelSensitiveWrite call from
// either UpdateChannel (v1) or UpdateChannelV2 (v2) turns the matching test
// below red while the other stays green — the two routes are independently
// wired into the real gate, not just independently unit-tested.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var channelSensitiveWriteRealChainDBCounter atomic.Int64

type realChainFixture struct {
	tenantID   string
	tenantSlug string
	adminID    int
	rootID     int
}

// setupChannelSensitiveWriteRealChain wires an isolated in-memory DB with
// one tenant, a tenant-admin (role 10) and a root (role 100) user in that
// tenant, and returns a request builder that serves through the REAL
// SetApiRouter/SetApiV2Router (both mounted on the same engine, exactly as
// cmd/server does) with the chosen identity injected into the gin session.
func setupChannelSensitiveWriteRealChain(t *testing.T) (*realChainFixture, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:channelsensitivewriterealchain%d?mode=memory&cache=shared", channelSensitiveWriteRealChainDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{
		&repo.User{}, &entity.Tenant{}, &repo.Channel{}, &entity.Ability{},
		&entity.ModelRateLimit{}, &entity.AdminPermissionGrant{}, &entity.AuditEvent{},
	} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatalf("migrate %T: %v", tbl, err)
		}
	}

	prevDB, prevRedis := repo.DB, common.RedisEnabled
	prevLogConsume := common.LogConsumeEnabled
	repo.DB = db
	repo.InitCol()
	common.RedisEnabled = false
	common.LogConsumeEnabled = false

	const tenantID = "real-chain-tenant"
	const tenantSlug = "real-chain-tenant-slug"
	tenant := &entity.Tenant{
		Id: tenantID, IDPOrgID: "real-chain-org", Slug: tenantSlug,
		Name: "Real Chain Tenant", Status: entity.TenantStatusEnabled,
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	adminRow := &repo.User{Username: "rc_admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Email: "rc_admin@local", TenantId: tenantID}
	rootRow := &repo.User{Username: "rc_root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Email: "rc_root@local", TenantId: tenantID}
	if err := db.Create(adminRow).Error; err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := db.Create(rootRow).Error; err != nil {
		t.Fatalf("create root: %v", err)
	}

	cleanup := func() {
		repo.DB, common.RedisEnabled = prevDB, prevRedis
		common.LogConsumeEnabled = prevLogConsume
		if sqlDB, _ := db.DB(); sqlDB != nil {
			_ = sqlDB.Close()
		}
	}

	return &realChainFixture{
		tenantID: tenantID, tenantSlug: tenantSlug,
		adminID: adminRow.Id, rootID: rootRow.Id,
	}, cleanup
}

// serveAs mounts a fresh session-injecting middleware in front of the
// shared engine's routes for this one request — sessions.Sessions is
// already registered once at setup, so this only needs to stamp session
// values before the request reaches it. A second gin.New() per request
// (mirroring root_seal_test.go's serve closure) keeps each call's session
// state isolated without re-registering the whole route table each time.
func (f *realChainFixture) serveAs(t *testing.T, userID int, role int, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var bodyReader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		bodyReader = bytes.NewReader(data)
	} else {
		bodyReader = bytes.NewReader(nil)
	}

	engine := gin.New()
	engine.Use(gin.Recovery())
	store := cookie.NewStore([]byte("channel-sensitive-write-real-chain-secret"))
	engine.Use(sessions.Sessions("session", store))
	engine.Use(func(c *gin.Context) {
		s := sessions.Default(c)
		var username string
		if role >= common.RoleRootUser {
			username = "rc_root"
		} else {
			username = "rc_admin"
		}
		s.Set("username", username)
		s.Set("role", role)
		s.Set("id", userID)
		s.Set("status", common.UserStatusEnabled)
		_ = s.Save()
		c.Next()
	})
	SetApiRouter(engine)
	SetApiV2Router(engine)

	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

func seedRealChainChannel(t *testing.T, f *realChainFixture, name string) *repo.Channel {
	t.Helper()
	ch := &repo.Channel{
		Name: name, TenantId: f.tenantID, Key: "sk-real-chain-" + name,
		Status: common.ChannelStatusEnabled, Type: 1, Models: "gpt-4", Group: "default",
		CreatedTime: common.GetTimestamp(),
	}
	if err := repo.DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	return ch
}

// TestChannelSensitiveWriteRealChain_V1_PUT proves the v1 gate through the
// production route table (SetApiRouter mounts PUT /api/channel/ under
// AdminAuth, router/api-router.go:157).
func TestChannelSensitiveWriteRealChain_V1_PUT(t *testing.T) {
	f, cleanup := setupChannelSensitiveWriteRealChain(t)
	defer cleanup()

	ch := seedRealChainChannel(t, f, "v1-real-chain")

	// A non-root admin without a grant is refused.
	w := f.serveAs(t, f.adminID, common.RoleAdminUser, http.MethodPut, "/api/channel/", map[string]interface{}{
		"id": ch.Id, "type": ch.Type, "base_url": "https://8.8.8.8",
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-root admin PUT /api/channel/: status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Success   bool   `json:"success"`
		ErrorCode string `json:"error_code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if resp.Success || resp.ErrorCode != "PERMISSION_DENIED" {
		t.Fatalf("body=%s, want success:false error_code:PERMISSION_DENIED", w.Body.String())
	}

	// Root sails through on the same route.
	wRoot := f.serveAs(t, f.rootID, common.RoleRootUser, http.MethodPut, "/api/channel/", map[string]interface{}{
		"id": ch.Id, "type": ch.Type, "base_url": "https://1.1.1.1",
	})
	if wRoot.Code != http.StatusOK {
		t.Fatalf("root PUT /api/channel/: status = %d, want 200; body=%s", wRoot.Code, wRoot.Body.String())
	}

	// Granting channel:sensitive_write to the admin unlocks the identical
	// request the first call above was refused on.
	if _, _, err := repo.CreatePermissionGrant(f.adminID, "channel", "sensitive_write", f.rootID); err != nil {
		t.Fatalf("seed grant: %v", err)
	}
	wGranted := f.serveAs(t, f.adminID, common.RoleAdminUser, http.MethodPut, "/api/channel/", map[string]interface{}{
		"id": ch.Id, "type": ch.Type, "base_url": "https://9.9.9.9",
	})
	if wGranted.Code != http.StatusOK {
		t.Fatalf("admin PUT /api/channel/ with an active grant: status = %d, want 200; body=%s", wGranted.Code, wGranted.Body.String())
	}
	reloaded, err := repo.GetChannelById(ch.Id, true)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if reloaded.BaseURL == nil || *reloaded.BaseURL != "https://9.9.9.9" {
		t.Errorf("expected base_url persisted once granted, got %v", reloaded.BaseURL)
	}
}

// TestChannelSensitiveWriteRealChain_V2_PUT proves the v2 gate through the
// production route table (SetApiV2Router mounts
// PUT /api/v2/:tenant_slug/channels/:id under AdminAuth + TenantSlugGuard
// on the tenantChannels group in router/api-v2-router.go).
func TestChannelSensitiveWriteRealChain_V2_PUT(t *testing.T) {
	f, cleanup := setupChannelSensitiveWriteRealChain(t)
	defer cleanup()

	ch := seedRealChainChannel(t, f, "v2-real-chain")
	path := fmt.Sprintf("/api/v2/%s/channels/%d", f.tenantSlug, ch.Id)

	// A non-root admin without a grant is refused.
	w := f.serveAs(t, f.adminID, common.RoleAdminUser, http.MethodPut, path, map[string]interface{}{
		"base_url": "https://8.8.8.8",
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-root admin PUT %s: status = %d, want 403; body=%s", path, w.Code, w.Body.String())
	}
	var resp struct {
		Success   bool   `json:"success"`
		ErrorCode string `json:"error_code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if resp.Success || resp.ErrorCode != "PERMISSION_DENIED" {
		t.Fatalf("body=%s, want success:false error_code:PERMISSION_DENIED", w.Body.String())
	}

	// Root sails through on the same route.
	wRoot := f.serveAs(t, f.rootID, common.RoleRootUser, http.MethodPut, path, map[string]interface{}{
		"base_url": "https://1.1.1.1",
	})
	if wRoot.Code != http.StatusOK {
		t.Fatalf("root PUT %s: status = %d, want 200; body=%s", path, wRoot.Code, wRoot.Body.String())
	}

	// Granting channel:sensitive_write to the admin unlocks the identical
	// request the first call above was refused on.
	if _, _, err := repo.CreatePermissionGrant(f.adminID, "channel", "sensitive_write", f.rootID); err != nil {
		t.Fatalf("seed grant: %v", err)
	}
	wGranted := f.serveAs(t, f.adminID, common.RoleAdminUser, http.MethodPut, path, map[string]interface{}{
		"base_url": "https://9.9.9.9",
	})
	if wGranted.Code != http.StatusOK {
		t.Fatalf("admin PUT %s with an active grant: status = %d, want 200; body=%s", path, wGranted.Code, wGranted.Body.String())
	}
	reloaded, err := repo.GetChannelById(ch.Id, true)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if reloaded.BaseURL == nil || *reloaded.BaseURL != "https://9.9.9.9" {
		t.Errorf("expected base_url persisted once granted, got %v", reloaded.BaseURL)
	}
}
