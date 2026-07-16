package handler

// v1_idor_sweep_test.go — extends cross-tenant IDOR coverage to the v1 channel
// ACTION handlers (test / balance / copy / fetch-models / ollama / multi-key)
// and the redemption invalid-prune. These were guarded by enforceTenantScope or
// a tenant-scoped repo variant but were previously untested. The structural
// completeness guard that FORCES every new tenant-scoped route into this sweep
// lives in internal/adapter/handler/router/idor_completeness_test.go (router
// package, to avoid a handler<->router import cycle).
//
// Reuses the fixtures from v1_cross_tenant_idor_test.go: v1Ctx, v1Body,
// seedV1VictimChannel, idorVictimTenant, and the V2TestContext harness.

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// TestV1Channel_ActionsCrossTenantIDOR asserts each by-id channel action denies
// a tenant admin operating on another tenant's channel. enforceTenantScope
// loads the channel then checks ownership BEFORE any upstream I/O, so the 403
// fires without a network call. Root-bypass of the shared helper is already
// covered by TestV1Channel_CrossTenantIDOR, so here we only pin the DENY — the
// security property — which keeps these cases hermetic (no upstream ping).
func TestV1Channel_ActionsCrossTenantIDOR(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	victim := seedV1VictimChannel(t, ctx) // in idorVictimTenant

	byID := []struct {
		name   string
		method string
		invoke func(c *gin.Context)
	}{
		{"TestChannel", http.MethodGet, TestChannel},
		{"UpdateChannelBalance", http.MethodGet, UpdateChannelBalance},
		{"CopyChannel", http.MethodPost, CopyChannel},
		{"FetchUpstreamModels", http.MethodGet, FetchUpstreamModels},
		{"OllamaVersion", http.MethodGet, OllamaVersion},
	}
	for _, tc := range byID {
		t.Run(tc.name, func(t *testing.T) {
			c, w := v1Ctx(tc.method, "/", nil, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
			c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(victim.Id)}}
			tc.invoke(c)
			if w.Code != http.StatusForbidden {
				t.Errorf("%s: tenant admin on another tenant's channel must 403, got code=%d body=%s",
					tc.name, w.Code, w.Body.String())
			}
		})
	}

	// ManageMultiKeys takes the channel id in the JSON body (channel_id).
	t.Run("ManageMultiKeys", func(t *testing.T) {
		c, w := v1Ctx(http.MethodPost, "/",
			map[string]interface{}{"channel_id": victim.Id, "action": "get_key_status"},
			common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
		ManageMultiKeys(c)
		if w.Code != http.StatusForbidden {
			t.Errorf("ManageMultiKeys: tenant admin on another tenant's channel must 403, got code=%d body=%s",
				w.Code, w.Body.String())
		}
	})
}

// TestV1Redemption_DeleteInvalidTenantScoped locks the DeleteInvalidRedemption
// fix: the bulk prune of spent/expired codes ran on the bare global DB, so a
// role-10 tenant admin purged EVERY tenant's invalid redemptions. A tenant
// admin must now prune only its own tenant; root still prunes globally.
func TestV1Redemption_DeleteInvalidTenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	seedInvalid := func(tenant string) *repo.Redemption {
		t.Helper()
		r := &repo.Redemption{
			UserId:      ctx.AdminUser.Id,
			TenantId:    tenant,
			Key:         common.GetRandomString(32),
			Name:        "spent-" + tenant,
			Quota:       500,
			Status:      common.RedemptionCodeStatusUsed, // "invalid" → eligible for prune
			CreatedTime: common.GetTimestamp(),
		}
		if err := ctx.DB.Create(r).Error; err != nil {
			t.Fatalf("seed invalid redemption: %v", err)
		}
		return r
	}
	own := seedInvalid(ctx.TenantID)
	victim := seedInvalid(idorVictimTenant)

	// (a)+(b) tenant admin prunes invalid codes: only its own tenant's are removed.
	c, w := v1Ctx(http.MethodDelete, "/api/redemption/invalid", nil,
		common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	DeleteInvalidRedemption(c)
	if v1Body(t, w)["success"] != true {
		t.Fatalf("(a) tenant admin invalid-prune must succeed, code=%d body=%s", w.Code, w.Body.String())
	}
	if n := v1RedemptionCount(ctx, own.Id); n != 0 {
		t.Errorf("(a) own invalid redemption not pruned, count=%d want 0", n)
	}
	if n := v1RedemptionCount(ctx, victim.Id); n != 1 {
		t.Errorf("(b) victim invalid redemption pruned cross-tenant, count=%d want 1", n)
	}

	// (c) root prunes across every tenant.
	c, _ = v1Ctx(http.MethodDelete, "/api/redemption/invalid", nil,
		common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	DeleteInvalidRedemption(c)
	if n := v1RedemptionCount(ctx, victim.Id); n != 0 {
		t.Errorf("(c) root invalid-prune must remove victim, count=%d want 0", n)
	}
}

func v1RedemptionCount(ctx *V2TestContext, id int) int64 {
	var n int64
	ctx.DB.Model(&repo.Redemption{}).Where("id = ?", id).Count(&n)
	return n
}
