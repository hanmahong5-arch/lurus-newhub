package handler

import (
	"encoding/json"
	"net/http"
	"testing"

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
