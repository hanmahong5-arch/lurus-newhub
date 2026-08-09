package repo

// cov_repo_redemption_tenant_test.go — DeleteInvalidRedemptionsByTenant is the
// tenant-scoped sibling of DeleteInvalidRedemptions: a per-tenant admin prune
// of spent/expired codes must never delete (or leak the existence of) another
// tenant's redemption codes.

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func repoSeedRedemption(t *testing.T, tenantID, key string, status int, expiredTime int64) int {
	t.Helper()
	r := &Redemption{
		TenantId:    tenantID,
		Key:         key,
		Status:      status,
		Name:        "r-" + key,
		Quota:       100,
		CreatedTime: common.GetTimestamp(),
		ExpiredTime: expiredTime,
	}
	if err := DB.Create(r).Error; err != nil {
		t.Fatalf("seed redemption: %v", err)
	}
	return r.Id
}

func TestDeleteInvalidRedemptionsByTenant_DeletesOwnUsedDisabledExpired_NotOthers(t *testing.T) {
	SetupTestDB(t)
	now := common.GetTimestamp()

	// tenant-a: used, disabled, expired-enabled → all invalid, should be deleted.
	aUsed := repoSeedRedemption(t, "tenant-a", common.GetUUID(), common.RedemptionCodeStatusUsed, 0)
	aDisabled := repoSeedRedemption(t, "tenant-a", common.GetUUID(), common.RedemptionCodeStatusDisabled, 0)
	aExpired := repoSeedRedemption(t, "tenant-a", common.GetUUID(), common.RedemptionCodeStatusEnabled, now-3600)
	// tenant-a: still-valid enabled, non-expired → must survive.
	aValid := repoSeedRedemption(t, "tenant-a", common.GetUUID(), common.RedemptionCodeStatusEnabled, now+3600)
	// tenant-a: enabled, never-expires (expired_time=0) → must survive.
	aNeverExpires := repoSeedRedemption(t, "tenant-a", common.GetUUID(), common.RedemptionCodeStatusEnabled, 0)

	// tenant-b: same invalid states, must NOT be touched by tenant-a's prune.
	bUsed := repoSeedRedemption(t, "tenant-b", common.GetUUID(), common.RedemptionCodeStatusUsed, 0)
	bExpired := repoSeedRedemption(t, "tenant-b", common.GetUUID(), common.RedemptionCodeStatusEnabled, now-3600)

	deleted, err := DeleteInvalidRedemptionsByTenant("tenant-a")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("want 3 rows deleted (used+disabled+expired), got %d", deleted)
	}

	for _, id := range []int{aUsed, aDisabled, aExpired} {
		var cnt int64
		DB.Model(&Redemption{}).Where("id = ?", id).Count(&cnt)
		if cnt != 0 {
			t.Errorf("tenant-a invalid redemption id=%d should have been deleted", id)
		}
	}
	for _, id := range []int{aValid, aNeverExpires} {
		var cnt int64
		DB.Model(&Redemption{}).Where("id = ?", id).Count(&cnt)
		if cnt != 1 {
			t.Errorf("tenant-a valid redemption id=%d must survive, count=%d", id, cnt)
		}
	}
	// Cross-tenant isolation: tenant-b rows must be completely untouched.
	for _, id := range []int{bUsed, bExpired} {
		var cnt int64
		DB.Model(&Redemption{}).Where("id = ?", id).Count(&cnt)
		if cnt != 1 {
			t.Errorf("tenant-b redemption id=%d leaked into tenant-a's prune, count=%d", id, cnt)
		}
	}
}

// TestDeleteInvalidRedemptionsByTenant_EmptyTenantIDInputGetsCoercedByGORMDefault
// documents a real GORM interaction: entity.Redemption.TenantId carries
// `gorm:"default:'default'"`, so Create() with the Go zero value (empty
// string) OMITS the column from the INSERT and the database applies its
// column default — the row is persisted with tenant_id="default", never "".
// FINDING: a caller that seeds/migrates a Redemption with TenantId="" (e.g.
// legacy-data backfill intending an explicit orphan marker) silently gets
// "default" instead — DeleteInvalidRedemptionsByTenant("") therefore matches
// NO rows (there is no such row) while DeleteInvalidRedemptionsByTenant(the
// literal string "default") is what actually reaches it. This mirrors the
// tenant-id-drift class of bug already tracked for this codebase (default vs
// a distinct literal), just via the GORM zero-value/default-tag path instead
// of application code.
func TestDeleteInvalidRedemptionsByTenant_EmptyTenantIDInputGetsCoercedByGORMDefault(t *testing.T) {
	SetupTestDB(t)

	coerced := repoSeedRedemption(t, "", common.GetUUID(), common.RedemptionCodeStatusUsed, 0)

	var stored Redemption
	if err := DB.First(&stored, "id = ?", coerced).Error; err != nil {
		t.Fatalf("readback: %v", err)
	}
	if stored.TenantId != "default" {
		t.Fatalf("FINDING assumption broken (re-verify): got tenant_id=%q, want GORM-coerced %q", stored.TenantId, "default")
	}

	// An empty-string tenant filter matches nothing (there is no "" row).
	deleted, err := DeleteInvalidRedemptionsByTenant("")
	if err != nil {
		t.Fatalf("delete(\"\"): %v", err)
	}
	if deleted != 0 {
		t.Fatalf("want 0 rows for tenant_id='' (no such row can exist via Create), got %d", deleted)
	}
	var stillThere int64
	DB.Model(&Redemption{}).Where("id = ?", coerced).Count(&stillThere)
	if stillThere != 1 {
		t.Fatal("the coerced row must not have been deleted by the empty-string call")
	}

	// The literal "default" tenant filter is what actually reaches the coerced row.
	deleted, err = DeleteInvalidRedemptionsByTenant("default")
	if err != nil {
		t.Fatalf("delete(\"default\"): %v", err)
	}
	if deleted != 1 {
		t.Fatalf("want 1 row deleted under the literal 'default' tenant, got %d", deleted)
	}
}

// TestDeleteInvalidRedemptionsByTenant_EmptyTenantIDDoesNotLeakOtherTenants
// pins the actual safety property: even though "" cannot match a real row
// (see above), a call with tenant_id="" must never touch a named tenant's
// invalid rows — the WHERE clause must not degrade into an unscoped sweep.
func TestDeleteInvalidRedemptionsByTenant_EmptyTenantIDDoesNotLeakOtherTenants(t *testing.T) {
	SetupTestDB(t)
	other := repoSeedRedemption(t, "tenant-real", common.GetUUID(), common.RedemptionCodeStatusUsed, 0)

	deleted, err := DeleteInvalidRedemptionsByTenant("")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("want 0 rows deleted for tenant_id='', got %d", deleted)
	}
	var otherCnt int64
	DB.Model(&Redemption{}).Where("id = ?", other).Count(&otherCnt)
	if otherCnt != 1 {
		t.Error("a real tenant's invalid row must not be deleted by an empty-tenant call")
	}
}

func TestDeleteInvalidRedemptionsByTenant_NoInvalidRowsReturnsZero(t *testing.T) {
	SetupTestDB(t)
	now := common.GetTimestamp()
	repoSeedRedemption(t, "tenant-clean", common.GetUUID(), common.RedemptionCodeStatusEnabled, now+3600)

	deleted, err := DeleteInvalidRedemptionsByTenant("tenant-clean")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("want 0 rows deleted when nothing is invalid, got %d", deleted)
	}
}

func TestDeleteInvalidRedemptionsByTenant_UnknownTenantReturnsZero(t *testing.T) {
	SetupTestDB(t)
	repoSeedRedemption(t, "tenant-real", common.GetUUID(), common.RedemptionCodeStatusUsed, 0)

	deleted, err := DeleteInvalidRedemptionsByTenant("tenant-does-not-exist")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("want 0 rows for a tenant with no rows at all, got %d", deleted)
	}
}
