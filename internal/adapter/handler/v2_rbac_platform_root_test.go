package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// TestSecurityRequirePlatformRoot exercises requirePlatformRoot's dual gate
// directly. The rows that matter are the tenant-admin ones: role 10 and the JWT
// string role "admin" are what requireTenantAdmin lets through, and letting them
// through here is exactly the escalation this helper exists to stop — the three
// callers write platform-global state from a /:tenant_slug route.
func TestSecurityRequirePlatformRoot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name  string
		role  int
		roles []string
		tcNil bool
		want  bool
	}{
		{"session root role", common.RoleRootUser, nil, false, true},
		{"session tenant admin role", common.RoleAdminUser, nil, false, false},
		{"session common user", common.RoleCommonUser, nil, false, false},
		{"jwt root string role", common.RoleCommonUser, []string{"root"}, false, true},
		{"jwt admin string role", common.RoleCommonUser, []string{"admin"}, false, false},
		{"jwt admin string role with role unset", 0, []string{"admin"}, false, false},
		{"jwt root among several roles", common.RoleCommonUser, []string{"admin", "root"}, false, true},
		{"common user empty roles", common.RoleCommonUser, []string{}, false, false},
		{"nil tenant context root role", common.RoleRootUser, nil, true, true},
		{"nil tenant context common role", common.RoleCommonUser, nil, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Set("role", tc.role)
			var tctx *middleware.TenantContext
			if !tc.tcNil {
				tctx = &middleware.TenantContext{Roles: tc.roles}
			}
			if got := requirePlatformRoot(c, tctx); got != tc.want {
				t.Errorf("requirePlatformRoot(role=%d, roles=%v, tcNil=%v) = %v, want %v",
					tc.role, tc.roles, tc.tcNil, got, tc.want)
			}
		})
	}
}

// TestSecurityTenantAdminCannotWriteGlobalState is the end-to-end half of the
// same contract: a role-10 tenant admin, authenticated for a tenant whose slug is
// in the URL, must be refused by all three v2 writes whose blast radius is the
// whole platform (global ratio maps / global option row / tenant-less model
// catalogue). Before the fix each of these returned 200/201 and the mutation
// landed for every tenant.
func TestSecurityTenantAdminCannotWriteGlobalState(t *testing.T) {
	tenantAdmin := map[string]string{"X-Test-Role": "admin"}

	t.Run("POST pricing", func(t *testing.T) {
		ctx := setupPricingWriteRouter(t)
		batch := []map[string]interface{}{{"model_name": "global-repricing-attempt", "model_ratio": 42.0}}

		w := postPricing(ctx, ctx.tenantSlug, batch, tenantAdmin)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 — a tenant admin must not reprice every tenant; body: %s", w.Code, w.Body.String())
		}

		// Nothing may have been persisted: the global option row must not exist.
		var count int64
		if err := ctx.db.Model(&repo.Option{}).Where("key = ?", "ModelRatio").Count(&count).Error; err != nil {
			t.Fatalf("count options: %v", err)
		}
		if count != 0 {
			t.Fatalf("ModelRatio option row was written despite the 403")
		}
	})

	t.Run("POST models", func(t *testing.T) {
		ctx := setupModelsWriteRouter(t)

		w := postModel(ctx, map[string]interface{}{"model_name": "global-catalogue-attempt"}, tenantAdmin)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 — a tenant admin must not add to the global catalogue; body: %s", w.Code, w.Body.String())
		}
		var count int64
		if err := ctx.db.Model(&repo.Model{}).Where("model_name = ?", "global-catalogue-attempt").Count(&count).Error; err != nil {
			t.Fatalf("count models: %v", err)
		}
		if count != 0 {
			t.Fatalf("model row was created despite the 403")
		}
	})

	t.Run("DELETE models", func(t *testing.T) {
		ctx := setupModelsWriteRouter(t)
		m := &repo.Model{ModelName: "global-catalogue-victim", Status: 1}
		if err := ctx.db.Create(m).Error; err != nil {
			t.Fatalf("seed model: %v", err)
		}

		w := deleteModel(ctx, m.Id, tenantAdmin)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 — a tenant admin must not delete from the global catalogue; body: %s", w.Code, w.Body.String())
		}
		var survivor repo.Model
		if err := ctx.db.First(&survivor, m.Id).Error; err != nil {
			t.Fatalf("model was soft-deleted despite the 403: %v", err)
		}
	})
}
