package router

// c13_tenant_channels_denial_test.go — cycle-13 W wiring oracle for L7's
// console-honesty decision on the tenant channels admin surface.
//
// GET /api/v2/:tenant_slug/channels was mounted behind middleware.AdminAuth(),
// whose refusal for an under-privileged session is v1's shape: HTTP 200
// {"success":false,"message":"无权进行此操作，权限不足"}. A browser reads 200
// as success and the envelope carries no error_code, so the console could not
// distinguish "you are not allowed" from "this tenant has no channels" — it
// rendered an empty table. The group now uses middleware.AdminSessionAuth(),
// which re-emits the same refusal as 403 + error_code PERMISSION_DENIED
// through the machinery RootAuth's v2 admin routes already use.
//
// The v1 shape is deliberately NOT changed: /api/channel/ is consumed by the
// Switch client, and this cycle's do-not-regress list keeps 200
// {success:false} there. The second test pins that, so a future "let's make
// all refusals 403" change cannot take v1 with it.
//
// Mutation (verified red): put middleware.AdminAuth() back on tenantChannels
// in api-v2-router.go — the v2 call answers 200 again.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var tenantChannelsDenialDBCounter atomic.Int64

// setupTenantChannelsDenialEngine mounts BOTH real route tables (v1 and v2)
// behind one session carrying a plain member identity in tenant "default",
// so the two refusal shapes below are observed on the same caller and the
// same process state — the only difference between them is the route.
func setupTenantChannelsDenialEngine(t *testing.T) *gin.Engine {
	t.Helper()

	seq := tenantChannelsDenialDBCounter.Add(1)
	db, err := gorm.Open(sqlite.Open(
		fmt.Sprintf("file:c13_tc_denial_%d?mode=memory&cache=shared", seq)), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&repo.User{}, &repo.Tenant{}, &repo.Channel{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	now := time.Now()
	if err := db.Create(&repo.Tenant{
		Id: "default", Name: "default", Slug: "default",
		Status: repo.TenantStatusEnabled, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	member := &repo.User{
		Id: 8500000 + int(seq), Username: fmt.Sprintf("c13-tc-%d", seq),
		DisplayName: "C13 Plain Member", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: fmt.Sprintf("c13-tc-%d@local", seq),
		TenantId: "default", Quota: 1_000_000,
	}
	if err := db.Create(member).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	prevDB := repo.DB
	prevSQLite, prevPG, prevRedis := common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled
	prevGlobalAPI, prevGlobalV2 := common.GlobalApiRateLimitEnable, common.GlobalV2RateLimitEnable
	repo.DB = db
	repo.InitCol()
	common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled = true, false, false
	common.GlobalApiRateLimitEnable, common.GlobalV2RateLimitEnable = false, false
	t.Cleanup(func() {
		repo.DB = prevDB
		common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled = prevSQLite, prevPG, prevRedis
		common.GlobalApiRateLimitEnable, common.GlobalV2RateLimitEnable = prevGlobalAPI, prevGlobalV2
		if sqlDB, dbErr := db.DB(); dbErr == nil && sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.Use(sessions.Sessions("session", cookie.NewStore([]byte("c13-tc-denial-secret"))))
	engine.Use(func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("username", member.Username)
		s.Set("role", member.Role)
		s.Set("id", member.Id)
		s.Set("status", common.UserStatusEnabled)
		_ = s.Save()
		c.Next()
	})
	SetApiRouter(engine)
	SetApiV2Router(engine)
	return engine
}

func tenantChannelsDenialCall(t *testing.T, engine *gin.Engine, path string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = "10.42.0.1:54321"
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: response is not JSON (status %d): %q", path, w.Code, w.Body.String())
	}
	return w.Code, body
}

func TestTenantChannelsV2_MemberGetsA403WithErrorCode(t *testing.T) {
	engine := setupTenantChannelsDenialEngine(t)
	status, body := tenantChannelsDenialCall(t, engine, "/api/v2/default/channels")

	if status != http.StatusForbidden {
		t.Errorf("GET /api/v2/default/channels for a role-%d session = %d, want 403 — a 200 refusal is read as success by every browser client; body=%v",
			common.RoleCommonUser, status, body)
	}
	code, _ := body["error_code"].(string)
	if code != "PERMISSION_DENIED" {
		t.Errorf("error_code = %q, want \"PERMISSION_DENIED\" — the console branches on this to tell a refusal from an empty tenant; body=%v", code, body)
	}
	if success, _ := body["success"].(bool); success {
		t.Errorf("success = true on a refusal; body=%v", body)
	}
	// The human sentence is kept, not replaced: a banned user and a
	// under-privileged one still get different messages.
	if msg, _ := body["message"].(string); msg == "" {
		t.Errorf("the refusal carries no message; body=%v", body)
	}
}

func TestV1ChannelList_RefusalShapeIsUnchanged(t *testing.T) {
	engine := setupTenantChannelsDenialEngine(t)
	status, body := tenantChannelsDenialCall(t, engine, "/api/channel/")

	// do-not-regress: the Switch client parses v1's 200 {success:false}. If
	// this ever becomes a 403, that is a contract break, not an improvement —
	// and it would happen silently if the v2 change above were applied to
	// AdminAuth itself rather than to a new v2-only middleware.
	if status != http.StatusOK {
		t.Errorf("GET /api/channel/ for a role-%d session = %d, want 200 — v1's refusal envelope is a consumed contract (this cycle's do-not-regress list); body=%v",
			common.RoleCommonUser, status, body)
	}
	if success, _ := body["success"].(bool); success {
		t.Errorf("v1 refusal has success=true; body=%v — the fixture did not actually get refused, so the status assertion above proves nothing", body)
	}
	if _, present := body["error_code"]; present {
		t.Errorf("v1 refusal now carries error_code %v — the v2 rewrite leaked onto the v1 surface", body["error_code"])
	}
}
