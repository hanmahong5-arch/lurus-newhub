package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting"

	"github.com/gin-gonic/gin"
)

// An exhausted (or, under CREDIT_POOL_REQUIRED=enforce, missing) credit pool
// rejects every request of the tenant. 2026-09-23: ~1,000 such 402s over
// 4.7 hours left no row in the logs. They must now leave one row per tenant
// per minute — visible, but not one per rejected request.

func poolGateRouterAs(tenantID string, userID int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("tenant_context", &TenantContext{TenantID: tenantID})
		c.Set("id", userID)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		c.Next()
	})
	r.Use(PoolBalanceCheck())
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	return r
}

func poolRejectionRows(t *testing.T, code string) []repo.Log {
	t.Helper()
	var logs []repo.Log
	if err := repo.LOG_DB.Where("type = ? AND other LIKE ?", repo.LogTypeError, "%"+code+"%").Find(&logs).Error; err != nil {
		t.Fatalf("query error logs: %v", err)
	}
	return logs
}

func TestPoolExhausted_LeavesOneErrorRowPerTenantPerMinute(t *testing.T) {
	cleanup := setupErrorLogDB(t)
	defer cleanup()

	for _, tenant := range []string{"t-pool-dry-a", "t-pool-dry-b"} {
		if err := repo.DB.Create(&entity.TenantCreditPool{TenantID: tenant, MaxBalance: 1000, CurrentBalance: 0}).Error; err != nil {
			t.Fatalf("seed pool %s: %v", tenant, err)
		}
	}

	for _, tenant := range []string{"t-pool-dry-a", "t-pool-dry-b"} {
		r := poolGateRouterAs(tenant, 7)
		for i := 0; i < 3; i++ {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
			if w.Code != http.StatusPaymentRequired {
				t.Fatalf("%s request %d: status %d, want 402; body=%s", tenant, i, w.Code, w.Body.String())
			}
		}
	}

	rows := poolRejectionRows(t, "pool_exhausted")
	if len(rows) != 2 {
		t.Fatalf("pool_exhausted error rows = %d, want 2 (one per tenant for 6 rejections)", len(rows))
	}
	for _, row := range rows {
		if !strings.Contains(row.Content, "credit pool exhausted") || row.UserId != 7 {
			t.Errorf("row = user %d %q, want user 7 and the exhaustion message", row.UserId, row.Content)
		}
	}
}

func TestPoolNotConfigured_Enforce_LeavesAnErrorRow(t *testing.T) {
	cleanup := setupErrorLogDB(t)
	defer cleanup()
	t.Setenv("CREDIT_POOL_REQUIRED", setting.CreditPoolRequiredEnforce)

	r := poolGateRouterAs("t-pool-none-enforce", 8)
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
		if w.Code != http.StatusPaymentRequired {
			t.Fatalf("status %d, want 402", w.Code)
		}
	}
	if rows := poolRejectionRows(t, "pool_not_configured"); len(rows) != 1 {
		t.Fatalf("pool_not_configured error rows = %d, want 1", len(rows))
	}
}
