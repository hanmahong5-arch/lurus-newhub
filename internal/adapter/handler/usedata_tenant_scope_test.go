package handler

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// GET /api/data/ is AdminAuth()-gated only, so a tenant admin reaches it. The
// quota_data table carries no tenant_id, so isolation has to come from the
// owning user — these tests pin BOTH branches of the handler (the grouped
// aggregate and the per-username detail list), because a half-scoped fix looks
// fixed while the aggregate still sums other tenants' usage.

const usedataScopeOtherTenant = "other-tenant-usedata"

// seedTwoTenantQuotaData gives each of two tenants one user and one quota_data
// row, both under the SAME username — usernames are only per-tenant unique
// (uk_users_tenant_username), so the username branch is a real leak path.
// Returns the tenant-A and tenant-B user ids.
func seedTwoTenantQuotaData(t *testing.T, ctx *V2TestContext) (int, int) {
	t.Helper()

	userA := &repo.User{Username: "usage-twin", DisplayName: "twin A", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, TenantId: ctx.TenantID}
	if err := ctx.DB.Create(userA).Error; err != nil {
		t.Fatalf("seed tenant-A user: %v", err)
	}
	userB := &repo.User{Username: "usage-twin", DisplayName: "twin B", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, TenantId: usedataScopeOtherTenant}
	if err := ctx.DB.Create(userB).Error; err != nil {
		t.Fatalf("seed tenant-B user: %v", err)
	}

	rows := []*repo.QuotaData{
		{UserID: userA.Id, Username: "usage-twin", ModelName: "gpt-4", CreatedAt: 1000, Count: 2, Quota: 100, TokenUsed: 50},
		{UserID: userB.Id, Username: "usage-twin", ModelName: "gpt-4", CreatedAt: 1000, Count: 3, Quota: 200, TokenUsed: 80},
	}
	for _, row := range rows {
		if err := ctx.DB.Table("quota_data").Create(row).Error; err != nil {
			t.Fatalf("seed quota_data: %v", err)
		}
	}
	return userA.Id, userB.Id
}

func usedataScopeItems(t *testing.T, ctxVals map[string]interface{}, query string) []interface{} {
	t.Helper()

	r := gin.New()
	r.Use(setAnalyticsCtx(ctxVals))
	r.GET("/api/data", GetAllQuotaDates)

	w := doAnalyticsGet(r, query)
	resp := AssertV2Success(t, w)
	items, ok := resp["data"].([]interface{})
	if !ok {
		t.Fatalf("expected data array, got %T (body %s)", resp["data"], w.Body.String())
	}
	return items
}

func TestUsedataTenantScope_AggregateExcludesOtherTenants(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	seedTwoTenantQuotaData(t, ctx)

	items := usedataScopeItems(t, map[string]interface{}{
		"id":        ctx.AdminUser.Id,
		"role":      common.RoleAdminUser, // tenant admin, NOT platform root
		"tenant_id": ctx.TenantID,
	}, "/api/data?start_timestamp=0&end_timestamp=2000")

	if len(items) != 1 {
		t.Fatalf("expected 1 grouped bucket, got %d: %v", len(items), items)
	}
	got := items[0].(map[string]interface{})
	if int(got["count"].(float64)) != 2 {
		t.Fatalf("aggregate leaked other tenant: expected count=2 (tenant A only), got %v", got["count"])
	}
	if int(got["quota"].(float64)) != 100 {
		t.Fatalf("aggregate leaked other tenant: expected quota=100 (tenant A only), got %v", got["quota"])
	}
	if int(got["token_used"].(float64)) != 50 {
		t.Fatalf("aggregate leaked other tenant: expected token_used=50 (tenant A only), got %v", got["token_used"])
	}
}

func TestUsedataTenantScope_ByUsernameExcludesOtherTenants(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	userAID, _ := seedTwoTenantQuotaData(t, ctx)

	items := usedataScopeItems(t, map[string]interface{}{
		"id":        ctx.AdminUser.Id,
		"role":      common.RoleAdminUser,
		"tenant_id": ctx.TenantID,
	}, "/api/data?start_timestamp=0&end_timestamp=2000&username=usage-twin")

	if len(items) != 1 {
		t.Fatalf("username branch leaked the other tenant's namesake, got %d rows: %v", len(items), items)
	}
	got := items[0].(map[string]interface{})
	if int(got["user_id"].(float64)) != userAID {
		t.Fatalf("expected tenant A's user_id=%d, got %v", userAID, got["user_id"])
	}
}

func TestUsedataTenantScope_RootSeesEveryTenant(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	seedTwoTenantQuotaData(t, ctx)

	rootCtx := map[string]interface{}{
		"id":        ctx.RootUser.Id,
		"role":      common.RoleRootUser,
		"tenant_id": ctx.TenantID,
	}

	items := usedataScopeItems(t, rootCtx, "/api/data?start_timestamp=0&end_timestamp=2000")
	if len(items) != 1 {
		t.Fatalf("expected 1 grouped bucket, got %d: %v", len(items), items)
	}
	got := items[0].(map[string]interface{})
	if int(got["count"].(float64)) != 5 {
		t.Fatalf("root aggregate must span tenants: expected count=5 (2+3), got %v", got["count"])
	}
	if int(got["quota"].(float64)) != 300 {
		t.Fatalf("root aggregate must span tenants: expected quota=300 (100+200), got %v", got["quota"])
	}

	byName := usedataScopeItems(t, rootCtx, "/api/data?start_timestamp=0&end_timestamp=2000&username=usage-twin")
	if len(byName) != 2 {
		t.Fatalf("root username lookup must span tenants: expected 2 rows, got %d: %v", len(byName), byName)
	}
}

// A non-root caller with no tenant in context must fail closed rather than fall
// back to the cross-tenant view.
func TestUsedataTenantScope_MissingTenantFailsClosed(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	seedTwoTenantQuotaData(t, ctx)

	items := usedataScopeItems(t, map[string]interface{}{
		"id":   ctx.AdminUser.Id,
		"role": common.RoleAdminUser,
	}, "/api/data?start_timestamp=0&end_timestamp=2000")

	if len(items) != 0 {
		t.Fatalf("missing tenant_id must match no rows, got %d: %v", len(items), items)
	}
}
