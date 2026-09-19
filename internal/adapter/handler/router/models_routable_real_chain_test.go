package router

// models_routable_real_chain_test.go — cycle-11 L1/W: proves GET
// /api/v2/:tenant_slug/models/routable is mounted under the tenantModels
// group (UserAuth + TenantSlugGuard) on the real SetApiV2Router route
// table, not a hand-built engine.
//
// Mutation: moving the GET("/routable") registration outside the
// tenantModels group (onto apiV2 directly, without UserAuth/TenantSlugGuard)
// turns the "own slug" (401 UNAUTHENTICATED half is untouched, but a signed
// -in caller reaching an unguarded route would no longer 403 on another
// tenant's slug) and "another tenant's slug" assertions red.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

var modelsRoutableRealChainDBCounter atomic.Int64

type modelsRoutableFixture struct {
	engine      *gin.Engine
	ownSlug     string
	otherSlug   string
	unknownSlug string
}

func setupModelsRoutableRealChain(t *testing.T) *modelsRoutableFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:modelsroutablerealchain%d?mode=memory&cache=shared", modelsRoutableRealChainDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &repo.Tenant{}, &repo.Channel{}, &repo.Ability{}} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatalf("migrate %T: %v", tbl, err)
		}
	}

	const ownTenantID = "r11-models-own"
	const otherTenantID = "r11-models-other"
	const ownSlug = "r11-models-own-slug"
	const otherSlug = "r11-models-other-slug"
	own := &repo.Tenant{Id: ownTenantID, IDPOrgID: "r11-models-own-org", Slug: ownSlug, Name: "R11 Own Tenant", Status: 1}
	other := &repo.Tenant{Id: otherTenantID, IDPOrgID: "r11-models-other-org", Slug: otherSlug, Name: "R11 Other Tenant", Status: 1}
	if err := db.Create(own).Error; err != nil {
		t.Fatalf("seed own tenant: %v", err)
	}
	if err := db.Create(other).Error; err != nil {
		t.Fatalf("seed other tenant: %v", err)
	}

	user := &repo.User{
		Username: "r11-models-user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: "r11-models-user@local", TenantId: ownTenantID,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	prevDB, prevRedis := repo.DB, common.RedisEnabled
	repo.DB = db
	repo.InitCol()
	common.RedisEnabled = false
	t.Cleanup(func() {
		repo.DB, common.RedisEnabled = prevDB, prevRedis
		if sqlDB, _ := db.DB(); sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	engine := gin.New()
	engine.Use(gin.Recovery())
	store := cookie.NewStore([]byte("models-routable-real-chain-secret"))
	engine.Use(sessions.Sessions("session", store))
	engine.Use(func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("username", user.Username)
		s.Set("role", user.Role)
		s.Set("id", user.Id)
		s.Set("status", common.UserStatusEnabled)
		_ = s.Save()
		c.Next()
	})
	SetApiV2Router(engine)

	return &modelsRoutableFixture{engine: engine, ownSlug: ownSlug, otherSlug: otherSlug, unknownSlug: "r11-models-does-not-exist"}
}

func TestModelsRoutable_RealChain(t *testing.T) {
	t.Run("no_session_401", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		engine := gin.New()
		engine.Use(gin.Recovery())
		store := cookie.NewStore([]byte("models-routable-real-chain-no-session-secret"))
		engine.Use(sessions.Sessions("session", store))
		SetApiV2Router(engine)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/anything/models/routable", nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("no session: status=%d, want 401; body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("own_slug_200_with_items_array", func(t *testing.T) {
		f := setupModelsRoutableRealChain(t)
		req := httptest.NewRequest(http.MethodGet, "/api/v2/"+f.ownSlug+"/models/routable", nil)
		w := httptest.NewRecorder()
		f.engine.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("own slug: status=%d, want 200; body=%s", w.Code, w.Body.String())
		}
		var env struct {
			Success bool `json:"success"`
			Data    struct {
				Items []interface{} `json:"items"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode: %v; body=%s", err, w.Body.String())
		}
		if !env.Success {
			t.Fatalf("own slug: success=false; body=%s", w.Body.String())
		}
		if env.Data.Items == nil {
			t.Fatalf("own slug: data.items is null, want an array (possibly empty); body=%s", w.Body.String())
		}
	})

	t.Run("other_tenant_slug_403_tenant_mismatch", func(t *testing.T) {
		f := setupModelsRoutableRealChain(t)
		req := httptest.NewRequest(http.MethodGet, "/api/v2/"+f.otherSlug+"/models/routable", nil)
		w := httptest.NewRecorder()
		f.engine.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("other tenant slug: status=%d, want 403; body=%s", w.Code, w.Body.String())
		}
		var env struct {
			ErrorCode string `json:"error_code"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode: %v; body=%s", err, w.Body.String())
		}
		if env.ErrorCode != "TENANT_MISMATCH" {
			t.Fatalf("other tenant slug: error_code=%q, want TENANT_MISMATCH; body=%s", env.ErrorCode, w.Body.String())
		}
	})

	t.Run("unknown_slug_404_tenant_not_found", func(t *testing.T) {
		f := setupModelsRoutableRealChain(t)
		req := httptest.NewRequest(http.MethodGet, "/api/v2/"+f.unknownSlug+"/models/routable", nil)
		w := httptest.NewRecorder()
		f.engine.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("unknown slug: status=%d, want 404; body=%s", w.Code, w.Body.String())
		}
		var env struct {
			ErrorCode string `json:"error_code"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode: %v; body=%s", err, w.Body.String())
		}
		if env.ErrorCode != "TENANT_NOT_FOUND" {
			t.Fatalf("unknown slug: error_code=%q, want TENANT_NOT_FOUND; body=%s", env.ErrorCode, w.Body.String())
		}
	})
}
