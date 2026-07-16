package repo

// tier2_per_tenant_unique_sqlite_test.go — hermetic constraint-shape tests for
// the TIER2 per-tenant unique rework (migration 025 + the struct-tag change on
// entity.User / repo.User). These hit the REAL database constraints created by
// AutoMigrate (setupSQLiteDB), not any handler-level pre-check:
//
//   * users.username is unique PER (tenant_id, username): the same username in
//     two tenants must insert cleanly; a duplicate within one tenant must be
//     rejected by the DB.
//   * users.access_token stays GLOBALLY unique (credential lookup key —
//     ValidateAccessToken has no tenant context).
//   * tokens.name / redemptions.name stay NON-unique even within a tenant —
//     AutoCreateDefaultToken names every user's token "default" and redemption
//     batches share one name, so a well-meaning (tenant_id, name) unique would
//     break both. This test pins that decision.

import (
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// isSQLiteUniqueViolation matches glebarez/sqlite's constraint error text.
func isSQLiteUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func TestUserUsername_PerTenantUnique(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	seedTenant(t, "default", "lurus", "Default")
	seedTenant(t, "tenant-b", "tenant-b", "Tenant B")

	// Same tenant, first insert: fine.
	seedUser(t, "alice", "alice@a.example", 1, 1, "default")

	// Cross-tenant duplicate username must be ALLOWED (per-tenant unique).
	if err := DB.Create(&User{
		Username: "alice", DisplayName: "alice-b", Role: 1, Status: 1,
		Email: "alice@b.example", TenantId: "tenant-b",
	}).Error; err != nil {
		t.Fatalf("cross-tenant duplicate username must insert, got: %v", err)
	}

	// Same-tenant duplicate must be REJECTED by the DB constraint itself.
	err := DB.Create(&User{
		Username: "alice", DisplayName: "alice-dup", Role: 1, Status: 1,
		Email: "alice-dup@a.example", TenantId: "default",
	}).Error
	if !isSQLiteUniqueViolation(err) {
		t.Fatalf("same-tenant duplicate username must trip the unique constraint, got: %v", err)
	}
}

func TestUserAccessToken_StaysGloballyUnique(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	seedTenant(t, "default", "lurus", "Default")
	seedTenant(t, "tenant-b", "tenant-b", "Tenant B")

	tok := "0123456789abcdef0123456789abcdef"
	u1 := &User{Username: "ga-user-1", Role: 1, Status: 1, TenantId: "default"}
	u1.SetAccessToken(tok)
	if err := DB.Create(u1).Error; err != nil {
		t.Fatalf("first user with access token: %v", err)
	}

	// Same access_token in ANOTHER tenant must still be rejected: the
	// credential is resolved without tenant context (ValidateAccessToken),
	// so its uniqueness deliberately stays global.
	u2 := &User{Username: "ga-user-2", Role: 1, Status: 1, TenantId: "tenant-b"}
	u2.SetAccessToken(tok)
	if err := DB.Create(u2).Error; !isSQLiteUniqueViolation(err) {
		t.Fatalf("duplicate access_token across tenants must trip the global unique, got: %v", err)
	}
}

func TestTokenAndRedemptionNames_StayNonUnique(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	seedTenant(t, "default", "lurus", "Default")
	u1 := seedUser(t, "nn-user-1", "nn1@example.com", 1, 1, "default")
	u2 := seedUser(t, "nn-user-2", "nn2@example.com", 1, 1, "default")

	// Two tokens named "default" in the SAME tenant (different users) — the
	// AutoCreateDefaultToken contract. A (tenant_id, name) unique would break it.
	for i, uid := range []int{u1.Id, u2.Id} {
		tk := &Token{
			UserId: uid, TenantId: "default", Key: common.GetRandomString(48),
			Status: common.TokenStatusEnabled, Name: "default",
			CreatedTime: common.GetTimestamp(), AccessedTime: common.GetTimestamp(),
			ExpiredTime: -1, UnlimitedQuota: true,
		}
		if err := DB.Create(tk).Error; err != nil {
			t.Fatalf("token %d named 'default' must insert (names are non-unique): %v", i+1, err)
		}
	}

	// Two redemptions sharing one name in the SAME tenant — the batch-creation
	// contract (handler creates up to 100 codes under one name).
	seedRedemptionRow(t, u1.Id, "default", "batch-2026-07", 100)
	seedRedemptionRow(t, u1.Id, "default", "batch-2026-07", 100)
}
