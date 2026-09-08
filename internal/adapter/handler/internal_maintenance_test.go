package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// setupMaintenanceRouter mirrors the production /internal/admin wiring for the
// rotate-due-tokens endpoint (same pattern as setupConvergenceRouter).
func setupMaintenanceRouter(t *testing.T) (*gin.Engine, func()) {
	t.Helper()
	router, cleanup := SetupIntegrationRouter(t)

	admin := router.Group("/internal/admin")
	admin.Use(middleware.InternalApiAuth())
	admin.Use(middleware.RequireScope(repo.ScopeAdmin))
	admin.POST("/rotate-due-tokens", InternalRotateDueTokens)

	return router, cleanup
}

// TestRotateDueTokens_RotatesDueLeavesFresh drives the manual rotation pass
// end-to-end through auth: the overdue token's key must change and its
// rotated_at must advance; the fresh token must be untouched; the response
// must report exactly one rotation.
func TestRotateDueTokens_RotatesDueLeavesFresh(t *testing.T) {
	router, cleanup := setupMaintenanceRouter(t)
	t.Cleanup(cleanup)

	now := common.GetTimestamp()

	due := &repo.Token{
		UserId: 1, TenantId: "default", Name: "rot-due",
		Key:    "rt-due-tok-0000000000000000000000000000",
		Status: common.TokenStatusEnabled, ExpiredTime: -1,
		AutoRotateDays: 1, RotatedAt: now - 3*86400,
	}
	seedToken(t, due)

	fresh := &repo.Token{
		UserId: 1, TenantId: "default", Name: "rot-fresh",
		Key:    "rt-fresh-tok-00000000000000000000000000",
		Status: common.TokenStatusEnabled, ExpiredTime: -1,
		AutoRotateDays: 30, RotatedAt: now,
	}
	seedToken(t, fresh)

	w := internalRequest(router, "POST", "/internal/admin/rotate-due-tokens", nil, convergenceAuthHeaders())
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", w.Code, w.Body.String())
	}

	var got struct {
		Success bool `json:"success"`
		Data    struct {
			Rotated int `json:"rotated"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Success {
		t.Errorf("expected success=true, body: %s", w.Body.String())
	}
	if got.Data.Rotated != 1 {
		t.Errorf("expected exactly 1 rotation, got %d", got.Data.Rotated)
	}

	var dueAfter, freshAfter repo.Token
	if err := repo.DB.First(&dueAfter, due.Id).Error; err != nil {
		t.Fatalf("reload due: %v", err)
	}
	if err := repo.DB.First(&freshAfter, fresh.Id).Error; err != nil {
		t.Fatalf("reload fresh: %v", err)
	}
	if dueAfter.Key == "rt-due-tok-0000000000000000000000000000" {
		t.Error("due token's key must have been rotated")
	}
	if dueAfter.RotatedAt == now-3*86400 {
		t.Error("due token's rotated_at must have advanced")
	}
	if freshAfter.Key != "rt-fresh-tok-00000000000000000000000000" {
		t.Errorf("fresh token's key must be untouched, got %q", freshAfter.Key)
	}
}

// TestRotateDueTokens_Idempotent: a second pass right after the first finds
// nothing due (the rotated token's baseline just advanced) and rotates zero.
func TestRotateDueTokens_Idempotent(t *testing.T) {
	router, cleanup := setupMaintenanceRouter(t)
	t.Cleanup(cleanup)

	now := common.GetTimestamp()
	seedToken(t, &repo.Token{
		UserId: 1, TenantId: "default", Name: "rot-idem",
		Key:    "rt-idem-tok-000000000000000000000000000",
		Status: common.TokenStatusEnabled, ExpiredTime: -1,
		AutoRotateDays: 1, RotatedAt: now - 2*86400,
	})

	first := internalRequest(router, "POST", "/internal/admin/rotate-due-tokens", nil, convergenceAuthHeaders())
	if first.Code != http.StatusOK {
		t.Fatalf("first pass: expected 200, got %d", first.Code)
	}
	second := internalRequest(router, "POST", "/internal/admin/rotate-due-tokens", nil, convergenceAuthHeaders())
	if second.Code != http.StatusOK {
		t.Fatalf("second pass: expected 200, got %d", second.Code)
	}

	var got struct {
		Data struct {
			Rotated int `json:"rotated"`
		} `json:"data"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal second: %v", err)
	}
	if got.Data.Rotated != 0 {
		t.Errorf("second pass must rotate 0 tokens, got %d", got.Data.Rotated)
	}
}

// TestRotateDueTokens_RequiresAuth: no key, no rotation.
func TestRotateDueTokens_RequiresAuth(t *testing.T) {
	router, cleanup := setupMaintenanceRouter(t)
	t.Cleanup(cleanup)

	w := internalRequest(router, "POST", "/internal/admin/rotate-due-tokens", nil, nil)
	if w.Code == http.StatusOK {
		t.Errorf("unauthenticated request must not return 200, got %d", w.Code)
	}
}

// setupPoolMaintenanceRouter mirrors setupMaintenanceRouter for the
// reset-due-pools endpoint: same admin-scoped mounting pattern, plus the two
// credit-pool tables (SetupIntegrationRouter's fixed table list does not
// include them).
func setupPoolMaintenanceRouter(t *testing.T) (*gin.Engine, func()) {
	t.Helper()
	router, cleanup := SetupIntegrationRouter(t)

	if err := repo.DB.AutoMigrate(&repo.TenantCreditPool{}, &repo.TenantCreditPoolDraw{}); err != nil {
		t.Fatalf("migrate pool tables: %v", err)
	}

	admin := router.Group("/internal/admin")
	admin.Use(middleware.InternalApiAuth())
	admin.Use(middleware.RequireScope(repo.ScopeAdmin))
	admin.POST("/reset-due-pools", InternalResetDuePools)

	return router, cleanup
}

// seedDuePoolForHandler creates a tenant pool that is due for a scheduled
// reset right now (daily period, next_reset_at one hour in the past).
func seedDuePoolForHandler(t *testing.T, tenantID string, maxBalance, currentBalance int64) *repo.TenantCreditPool {
	t.Helper()
	pool, err := repo.CreateTenantCreditPool(tenantID, 1, maxBalance, repo.PoolResetDaily, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	due := time.Now().UTC().Add(-time.Hour)
	if err := repo.DB.Model(&repo.TenantCreditPool{}).Where("id = ?", pool.ID).
		Updates(map[string]interface{}{"current_balance": currentBalance, "next_reset_at": due}).Error; err != nil {
		t.Fatalf("seed due state: %v", err)
	}
	return pool
}

// TestResetDuePools_ObserveListsDueWithNoDraw: ?mode=observe lists the due
// pool with action=observed and writes no draw row.
func TestResetDuePools_ObserveListsDueWithNoDraw(t *testing.T) {
	router, cleanup := setupPoolMaintenanceRouter(t)
	t.Cleanup(cleanup)

	pool := seedDuePoolForHandler(t, "t-h-reset-obs", 1000, 100)

	w := internalRequest(router, "POST", "/internal/admin/reset-due-pools?mode=observe", nil, convergenceAuthHeaders())
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", w.Code, w.Body.String())
	}

	var got struct {
		Success bool `json:"success"`
		Data    struct {
			Mode  string `json:"mode"`
			Pools []struct {
				TenantID string `json:"tenant_id"`
				PoolID   int64  `json:"pool_id"`
				Delta    int64  `json:"delta"`
				Action   string `json:"action"`
			} `json:"pools"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Success || got.Data.Mode != "observe" {
		t.Fatalf("expected success=true mode=observe, body: %s", w.Body.String())
	}
	if len(got.Data.Pools) != 1 || got.Data.Pools[0].TenantID != "t-h-reset-obs" ||
		got.Data.Pools[0].Action != "observed" || got.Data.Pools[0].Delta != 900 {
		t.Errorf("pools = %+v, want one observed t-h-reset-obs delta=900", got.Data.Pools)
	}

	reload, err := repo.GetTenantCreditPool("t-h-reset-obs")
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if reload.CurrentBalance != 100 {
		t.Errorf("balance = %d, want 100 (observe must not write)", reload.CurrentBalance)
	}
	_, total, derr := repo.ListPoolDraws(pool.ID, 0, 10)
	if derr != nil {
		t.Fatalf("list draws: %v", derr)
	}
	if total != 0 {
		t.Errorf("observe wrote %d draws, want 0", total)
	}
}

// TestResetDuePools_EnforceWritesDraw: ?mode=enforce refills the pool and
// writes exactly one reset draw.
func TestResetDuePools_EnforceWritesDraw(t *testing.T) {
	router, cleanup := setupPoolMaintenanceRouter(t)
	t.Cleanup(cleanup)

	pool := seedDuePoolForHandler(t, "t-h-reset-enf", 1000, 100)

	w := internalRequest(router, "POST", "/internal/admin/reset-due-pools?mode=enforce", nil, convergenceAuthHeaders())
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", w.Code, w.Body.String())
	}

	var got struct {
		Data struct {
			Mode  string `json:"mode"`
			Pools []struct {
				Action string `json:"action"`
				Delta  int64  `json:"delta"`
			} `json:"pools"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Data.Mode != "enforce" || len(got.Data.Pools) != 1 || got.Data.Pools[0].Action != "reset" || got.Data.Pools[0].Delta != 900 {
		t.Errorf("data = %+v, want mode=enforce one reset delta=900", got.Data)
	}

	reload, err := repo.GetTenantCreditPool("t-h-reset-enf")
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if reload.CurrentBalance != 1000 {
		t.Errorf("balance = %d, want 1000 (refilled to ceiling)", reload.CurrentBalance)
	}
	_, total, derr := repo.ListPoolDraws(pool.ID, 0, 10)
	if derr != nil {
		t.Fatalf("list draws: %v", derr)
	}
	if total != 1 {
		t.Errorf("draw count = %d, want 1", total)
	}
}

// TestResetDuePools_RequiresAdminScope: a key without the admin scope must
// be rejected (403), not silently fall through to a plain-auth 200.
func TestResetDuePools_RequiresAdminScope(t *testing.T) {
	router, cleanup := setupPoolMaintenanceRouter(t)
	t.Cleanup(cleanup)

	seedDuePoolForHandler(t, "t-h-reset-scope", 1000, 100)

	w := internalRequest(router, "POST", "/internal/admin/reset-due-pools?mode=observe", nil,
		map[string]string{"X-API-Key": testApiKeyReadOnly})
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for a non-admin-scoped key, got %d, body: %s", w.Code, w.Body.String())
	}
}

// TestResetDuePools_UnrecognisedModeIs400: findings L4 item 4 — a supplied
// but unrecognised ?mode= value (typo, wrong case, anything other than the
// two accepted literals) must be rejected with 400, not silently coerced to
// observe. An omitted mode is a different case (falls back to the env
// default) and is NOT covered by this lock.
func TestResetDuePools_UnrecognisedModeIs400(t *testing.T) {
	router, cleanup := setupPoolMaintenanceRouter(t)
	t.Cleanup(cleanup)

	pool := seedDuePoolForHandler(t, "t-h-reset-badmode", 1000, 100)

	w := internalRequest(router, "POST", "/internal/admin/reset-due-pools?mode=Observe", nil, convergenceAuthHeaders())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unrecognised mode, got %d, body: %s", w.Code, w.Body.String())
	}

	var got struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Success {
		t.Error("expected success=false")
	}
	if !strings.Contains(got.Message, "observe") || !strings.Contains(got.Message, "enforce") {
		t.Errorf("message should name the accepted values, got %q", got.Message)
	}

	// The rejected call must not have run a pass at all — the due pool is
	// untouched, not even evaluated as "observed".
	reload, err := repo.GetTenantCreditPool("t-h-reset-badmode")
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if reload.CurrentBalance != 100 {
		t.Errorf("balance = %d, want 100 (rejected mode must not run a pass)", reload.CurrentBalance)
	}
	_, total, derr := repo.ListPoolDraws(pool.ID, 0, 10)
	if derr != nil {
		t.Fatalf("list draws: %v", derr)
	}
	if total != 0 {
		t.Errorf("draw count = %d, want 0", total)
	}
}
