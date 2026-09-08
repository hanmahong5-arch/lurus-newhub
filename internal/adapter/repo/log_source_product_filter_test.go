package repo

// log_source_product_filter_test.go — the read side of the cross-product
// attribution filter (Workstream 0): a sibling product team must be able to
// pull its own spend back out of the logs it wrote the tag into.

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestGetUserLogsWithParams_SourceProductFilter(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "sourceproduct-user", "sourceproduct@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-sourceproduct")
	now := common.GetTimestamp()
	rows := []*Log{
		{UserId: u.Id, TenantId: u.TenantId, Type: LogTypeConsume, ModelName: "gpt-4o", Quota: 10, CreatedAt: now, Other: `{"source_product":"lutu"}`},
		{UserId: u.Id, TenantId: u.TenantId, Type: LogTypeConsume, ModelName: "gpt-4o", Quota: 20, CreatedAt: now, Other: `{"source_product":"switch"}`},
		// Legacy/unattributed row: Other is "" (not valid JSON), same as rows
		// written before this workstream shipped — jsonOtherTextExpr must
		// not error on it.
		{UserId: u.Id, TenantId: u.TenantId, Type: LogTypeConsume, ModelName: "gpt-4o", Quota: 30, CreatedAt: now, Other: ""},
	}
	for i, r := range rows {
		if err := LOG_DB.Create(r).Error; err != nil {
			t.Fatalf("seed log %d: %v", i, err)
		}
	}

	// Filtered: only the lutu-tagged row.
	filtered, total, err := GetUserLogsWithParams(ForTenant(u.TenantId), &LogQueryParams{
		UserID: u.Id, SourceProduct: "lutu", Offset: 0, Limit: 10,
	})
	if err != nil {
		t.Fatalf("GetUserLogsWithParams(lutu): %v", err)
	}
	if total != 1 {
		t.Fatalf("total = %d, want 1 for source_product=lutu", total)
	}
	if len(filtered) != 1 || filtered[0].Quota != 10 {
		t.Errorf("filtered rows = %+v, want the single quota=10 lutu row", filtered)
	}

	// Unfiltered: all three rows, including the untagged legacy one.
	all, total, err := GetUserLogsWithParams(ForTenant(u.TenantId), &LogQueryParams{
		UserID: u.Id, Offset: 0, Limit: 10,
	})
	if err != nil {
		t.Fatalf("GetUserLogsWithParams(unfiltered): %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3 with no source_product filter", total)
	}
	_ = all
}

func TestGetTenantLogsWithParams_SourceProductFilter(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	tenant := seedTenant(t, "tenant-sp-admin", "tenant-sp-admin", "SP Admin Tenant")
	u := seedUser(t, "sp-admin-user", "spadmin@test.local", common.RoleCommonUser, common.UserStatusEnabled, tenant.Id)
	now := common.GetTimestamp()
	rows := []*Log{
		{UserId: u.Id, TenantId: tenant.Id, Type: LogTypeConsume, Quota: 5, CreatedAt: now, Other: `{"source_product":"kova"}`},
		{UserId: u.Id, TenantId: tenant.Id, Type: LogTypeConsume, Quota: 7, CreatedAt: now, Other: `{"source_product":"switch"}`},
	}
	for i, r := range rows {
		if err := LOG_DB.Create(r).Error; err != nil {
			t.Fatalf("seed log %d: %v", i, err)
		}
	}

	logs, total, err := GetTenantLogsWithParams(ForTenant(tenant.Id), &LogQueryParams{
		SourceProduct: "kova", Offset: 0, Limit: 10,
	})
	if err != nil {
		t.Fatalf("GetTenantLogsWithParams(kova): %v", err)
	}
	if total != 1 || len(logs) != 1 || logs[0].Quota != 5 {
		t.Errorf("logs = %+v total=%d, want exactly the quota=5 kova row", logs, total)
	}
}
