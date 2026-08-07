package repo

// Covers GetTenantMonthlyQuotaUsed's untested branches: the existing
// sqlite_repo_extra_test.go case only calls it against an empty log table
// with a well-formed period, so it never exercises (a) the invalid-period
// parse-error return, or (b) that the SUM is actually scoped to the tenant
// (i.e. does not leak another tenant's consumption) and to the LogTypeConsume
// type + month boundaries.

import (
	"strings"
	"testing"
)

func r5coreSeedConsumeLog(t *testing.T, tenantID string, quota int, createdAt int64) {
	t.Helper()
	log := &Log{
		TenantId:  tenantID,
		Type:      LogTypeConsume,
		Quota:     quota,
		CreatedAt: createdAt,
	}
	if err := LOG_DB.Create(log).Error; err != nil {
		t.Fatalf("seed consume log for tenant %q: %v", tenantID, err)
	}
}

func TestGetTenantMonthlyQuotaUsed_InvalidPeriodErrors(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ten := seedTenant(t, "bad-period-tenant", "bad-period", "Bad Period Tenant")

	for _, period := range []string{"", "not-a-period", "2026-13", "05-2026"} {
		if _, err := GetTenantMonthlyQuotaUsed(ten.Id, period); err == nil {
			t.Errorf("GetTenantMonthlyQuotaUsed(period=%q) expected a parse error, got nil", period)
		} else if !strings.Contains(err.Error(), "invalid period") {
			t.Errorf("GetTenantMonthlyQuotaUsed(period=%q) err = %v, want it to mention the invalid period", period, err)
		}
	}
}

func TestGetTenantMonthlyQuotaUsed_ScopedToTenantAndMonth(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	tenA := seedTenant(t, "quota-tenant-a", "quota-a", "Quota Tenant A")
	tenB := seedTenant(t, "quota-tenant-b", "quota-b", "Quota Tenant B")

	// May 2026 UTC boundaries: 2026-05-01T00:00:00Z .. 2026-05-31T23:59:59Z.
	mayStart := int64(1777593600) // 2026-05-01T00:00:00Z
	mayMid := mayStart + 3600
	juneStart := int64(1780272000) // 2026-06-01T00:00:00Z (just outside May)

	r5coreSeedConsumeLog(t, tenA.Id, 100, mayMid)
	r5coreSeedConsumeLog(t, tenA.Id, 50, mayMid+10)
	// Belongs to a different tenant — must not leak into tenant A's sum.
	r5coreSeedConsumeLog(t, tenB.Id, 9999, mayMid)
	// Belongs to tenant A but outside the May window — must not be counted.
	r5coreSeedConsumeLog(t, tenA.Id, 7777, juneStart)

	used, err := GetTenantMonthlyQuotaUsed(tenA.Id, "2026-05")
	if err != nil {
		t.Fatalf("GetTenantMonthlyQuotaUsed: %v", err)
	}
	if used != 150 {
		t.Errorf("GetTenantMonthlyQuotaUsed(tenant A, 2026-05) = %d, want 150 (must exclude tenant B and out-of-month rows)", used)
	}

	usedB, err := GetTenantMonthlyQuotaUsed(tenB.Id, "2026-05")
	if err != nil {
		t.Fatalf("GetTenantMonthlyQuotaUsed(tenant B): %v", err)
	}
	if usedB != 9999 {
		t.Errorf("GetTenantMonthlyQuotaUsed(tenant B, 2026-05) = %d, want 9999", usedB)
	}

	// A tenant with zero matching rows must report 0, not an error.
	usedEmpty, err := GetTenantMonthlyQuotaUsed(tenA.Id, "2020-01")
	if err != nil {
		t.Fatalf("GetTenantMonthlyQuotaUsed(no rows in period): %v", err)
	}
	if usedEmpty != 0 {
		t.Errorf("GetTenantMonthlyQuotaUsed(no matching rows) = %d, want 0", usedEmpty)
	}
}
