package repo

// token_autocreate_owner_test.go — L2: AutoCreateDefaultToken must bind the
// bootstrap token to the OWNING USER'S tenant, not the literal "default"
// tenant. Before this fix every playground/session token was hardcoded into
// "default" regardless of who it was minted for, which pools every tenant's
// auto-created spend onto the bootstrap tenant's credit pool (see the
// two-pool companion in internal/app for the money-level consequence).

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestAutoCreateDefaultToken_UsesOwnerTenant(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	user := User{TenantId: "acme", Username: "acme-owner-" + common.GetRandomString(6), Status: common.UserStatusEnabled, Group: "default"}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	tok, err := AutoCreateDefaultToken(user.Id)
	if err != nil {
		t.Fatalf("AutoCreateDefaultToken: %v", err)
	}
	if tok.TenantId != "acme" {
		t.Errorf("token tenant_id = %q, want owner's tenant %q", tok.TenantId, "acme")
	}
}

func TestAutoCreateDefaultToken_FallsBackToDefaultWhenOwnerTenantEmpty(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	user := User{Username: "no-tenant-owner-" + common.GetRandomString(6), Status: common.UserStatusEnabled, Group: "default"}
	if err := DB.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	// The column default only applies at INSERT time when the field is the
	// Go zero value; force an explicit empty string afterward to simulate a
	// pre-backfill row whose tenant_id is genuinely empty.
	if err := DB.Model(&User{}).Where("id = ?", user.Id).Update("tenant_id", "").Error; err != nil {
		t.Fatalf("force empty tenant: %v", err)
	}

	tok, err := AutoCreateDefaultToken(user.Id)
	if err != nil {
		t.Fatalf("AutoCreateDefaultToken: %v", err)
	}
	if tok.TenantId != "default" {
		t.Errorf("token tenant_id = %q, want fallback %q", tok.TenantId, "default")
	}
}
