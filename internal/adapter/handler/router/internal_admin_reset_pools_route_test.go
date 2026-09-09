package router

// internal_admin_reset_pools_route_test.go — proves POST
// /internal/admin/reset-due-pools is actually mounted under
// middleware.RequireScope(repo.ScopeAdmin) in the real production router
// wiring (SetInternalApiRouter), not just reachable with any valid key. A
// stubbed-out handler could pass a 401-without-a-key check; only a genuine
// RequireScope(ScopeAdmin) registration produces the 403 in the second test
// below for a key that has every OTHER scope but admin.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var resetPoolsRouteDBCounter atomic.Int64

// resetPoolsRouteEngine wires the real SetInternalApiRouter against a fresh
// in-memory sqlite DB carrying InternalApiKey plus the two credit-pool
// tables, and returns a cleanup that restores repo.DB.
func resetPoolsRouteEngine(t *testing.T) *gin.Engine {
	t.Helper()
	common.RedisEnabled = false
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:reset_pools_route_%d?mode=memory&cache=shared", resetPoolsRouteDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&entity.InternalApiKey{}, &entity.TenantCreditPool{}, &entity.TenantCreditPoolDraw{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prevDB := repo.DB
	repo.DB = db
	t.Cleanup(func() {
		repo.DB = prevDB
		if sqlDB, _ := db.DB(); sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	engine := gin.New()
	SetInternalApiRouter(engine)
	return engine
}

// TestResetDuePoolsRoute_MissingKeyRejected proves the route exists (401,
// not 404) and is gated by InternalApiAuth like every other /internal route.
func TestResetDuePoolsRoute_MissingKeyRejected(t *testing.T) {
	engine := resetPoolsRouteEngine(t)

	req := httptest.NewRequest(http.MethodPost, "/internal/admin/reset-due-pools", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without X-API-Key, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestResetDuePoolsRoute_NonAdminScopeRejected is the RequireScope(ScopeAdmin)
// assertion: a key with every scope EXCEPT admin must be rejected with 403,
// not fall through to the handler.
func TestResetDuePoolsRoute_NonAdminScopeRejected(t *testing.T) {
	engine := resetPoolsRouteEngine(t)

	rawKey, _, err := repo.CreateInternalApiKey("non-admin-probe", []string{
		repo.ScopeUserRead, repo.ScopeUserWrite, repo.ScopeQuotaRead, repo.ScopeQuotaWrite,
		repo.ScopeBalanceRead, repo.ScopeBalanceWrite, repo.ScopeTokenRead, repo.ScopeTokenWrite,
		repo.ScopeLogRead, repo.ScopeModelRead, repo.ScopeProvisioning,
	}, 0, 0, "every scope but admin")
	if err != nil {
		t.Fatalf("create key: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/internal/admin/reset-due-pools?mode=observe", nil)
	req.Header.Set("X-API-Key", rawKey)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a key without ScopeAdmin, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestResetDuePoolsRoute_AdminScopeReachesHandler proves the positive case:
// a ScopeAdmin key reaches InternalResetDuePools and gets a normal 200 with
// an empty pool list (no due pools seeded).
func TestResetDuePoolsRoute_AdminScopeReachesHandler(t *testing.T) {
	engine := resetPoolsRouteEngine(t)

	rawKey, _, err := repo.CreateInternalApiKey("admin-probe", []string{repo.ScopeAdmin}, 0, 0, "admin only")
	if err != nil {
		t.Fatalf("create key: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/internal/admin/reset-due-pools?mode=observe", nil)
	req.Header.Set("X-API-Key", rawKey)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for a ScopeAdmin key, got %d body=%s", w.Code, w.Body.String())
	}
}
