package router

// tenant_invites_list_real_chain_test.go — cycle-11 L6/W: proves GET
// /api/v2/admin/tenants/:id/invites is mounted under adminRoute's
// RootJWTAuth (api-v2-router.go's tenantMgmt group), through the real
// SetApiV2Router mount — same session-seeding convention
// v2_admin_system_tasks_mount_test.go uses (no Authorization header, so
// RootJWTAuth falls back to the session-based authHelper(RoleRootUser)).
//
// Mutation: mounting the GET outside tenantMgmt (e.g. directly on
// adminRoute without RootJWTAuth re-applied, or on a group lacking it)
// turns the non-root sub-test red.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
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

var tenantInvitesListMountDBCounter atomic.Int64

var hex32Re = regexp.MustCompile(`[0-9a-f]{32}`)

// tenantInvitesListMountRouter builds the real SetApiV2Router engine over an
// isolated hermetic DB seeded with one tenant, with an optional
// session-seeding middleware ahead of it (role < 0 = no session).
func tenantInvitesListMountRouter(t *testing.T, role int) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:tenantinviteslistmount%d?mode=memory&cache=shared", tenantInvitesListMountDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &repo.Tenant{}, &repo.TenantInvite{}} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatalf("migrate %T: %v", tbl, err)
		}
	}

	const tenantID = "r11-invites-tenant"
	tenant := &repo.Tenant{Id: tenantID, Name: "R11 Invites Tenant", Status: 1}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	prevDB, prevLogDB, prevRedis := repo.DB, repo.LOG_DB, common.RedisEnabled
	prevLogConsume := common.LogConsumeEnabled
	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.RedisEnabled = false
	common.LogConsumeEnabled = false
	t.Cleanup(func() {
		repo.DB, repo.LOG_DB, common.RedisEnabled = prevDB, prevLogDB, prevRedis
		common.LogConsumeEnabled = prevLogConsume
		if sqlDB, _ := db.DB(); sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	engine := gin.New()
	engine.Use(gin.Recovery())
	store := cookie.NewStore([]byte("tenant-invites-list-mount-test-secret"))
	engine.Use(sessions.Sessions("session", store))
	if role >= 0 {
		engine.Use(func(c *gin.Context) {
			s := sessions.Default(c)
			s.Set("username", "r11_invites_actor")
			s.Set("role", role)
			s.Set("id", 1)
			s.Set("status", common.UserStatusEnabled)
			_ = s.Save()
			c.Next()
		})
	}
	SetApiV2Router(engine)
	return engine, tenantID
}

func TestTenantInvitesList_RealChain(t *testing.T) {
	t.Run("non_root_rejected", func(t *testing.T) {
		engine, tenantID := tenantInvitesListMountRouter(t, common.RoleAdminUser)
		req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/tenants/"+tenantID+"/invites", nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		var env map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode envelope: %v; body=%s", err, w.Body.String())
		}
		if success, _ := env["success"].(bool); success {
			t.Fatalf("non-root admin session: success=true, want false (root-only route); status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("root_lists_with_no_full_code", func(t *testing.T) {
		engine, tenantID := tenantInvitesListMountRouter(t, common.RoleRootUser)
		base := "/api/v2/admin/tenants/" + tenantID

		// Issue an invite first so the list is non-empty.
		issueReq := httptest.NewRequest(http.MethodPost, base+"/invites", nil)
		issueW := httptest.NewRecorder()
		engine.ServeHTTP(issueW, issueReq)
		if issueW.Code != http.StatusCreated {
			t.Fatalf("root issue invite: status=%d, want 201; body=%s", issueW.Code, issueW.Body.String())
		}

		listReq := httptest.NewRequest(http.MethodGet, base+"/invites", nil)
		listW := httptest.NewRecorder()
		engine.ServeHTTP(listW, listReq)
		if listW.Code != http.StatusOK {
			t.Fatalf("root list invites: status=%d, want 200; body=%s", listW.Code, listW.Body.String())
		}

		var env map[string]interface{}
		if err := json.Unmarshal(listW.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode envelope: %v; body=%s", err, listW.Body.String())
		}
		if success, _ := env["success"].(bool); !success {
			t.Fatalf("root list invites: success=false, want true; body=%s", listW.Body.String())
		}
		data, ok := env["data"].(map[string]interface{})
		if !ok {
			t.Fatalf("root list invites: missing data object; body=%s", listW.Body.String())
		}
		invites, ok := data["invites"].([]interface{})
		if !ok || len(invites) == 0 {
			t.Fatalf("root list invites: data.invites is not a non-empty array; body=%s", listW.Body.String())
		}

		if hex32Re.MatchString(listW.Body.String()) {
			t.Fatalf("root list invites body contains a 32-hex-char string — the full invite code must never appear in the list projection; body=%s", listW.Body.String())
		}
	})
}
