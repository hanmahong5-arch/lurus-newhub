package repo

// user_cache_tenant_role_test.go — tenant_id and tenant_role must survive the
// Redis user-cache round trip. On a cache hit the session path builds its
// TenantContext from this struct alone; a dropped field would silently turn a
// customer's tenant admin into a plain member (role lost) or park a non-default
// tenant's user in "default" (tenant lost).

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

func TestUserCache_TenantFieldsSurviveRedisHit(t *testing.T) {
	repoWithMiniRedis(t)

	u := User{Id: 4242, TenantId: "tenant-acme", TenantRole: entity.TenantRoleAdmin, Username: "u", Status: 1, Role: 1}
	if err := updateUserCache(u); err != nil {
		t.Fatalf("updateUserCache: %v", err)
	}

	// GetUserCache must be served by Redis: there is no DB in this test, so a
	// miss would surface as an error.
	got, err := GetUserCache(4242)
	if err != nil {
		t.Fatalf("GetUserCache (expected cache hit): %v", err)
	}
	if got.TenantId != "tenant-acme" {
		t.Errorf("TenantId = %q after cache hit, want tenant-acme", got.TenantId)
	}
	if got.TenantRole != entity.TenantRoleAdmin {
		t.Errorf("TenantRole = %q after cache hit, want %q", got.TenantRole, entity.TenantRoleAdmin)
	}
	if got.Role != 1 {
		t.Errorf("integer Role = %d, want 1 (tenant_role must not touch it)", got.Role)
	}
}

func TestUserCache_EmptyTenantRoleRoundTripsEmpty(t *testing.T) {
	repoWithMiniRedis(t)

	if err := updateUserCache(User{Id: 4243, TenantId: "tenant-acme", Username: "plain"}); err != nil {
		t.Fatalf("updateUserCache: %v", err)
	}
	got, err := cacheGetUserBase(4243)
	if err != nil {
		t.Fatalf("cacheGetUserBase: %v", err)
	}
	if got.TenantRole != "" || got.TenantId != "tenant-acme" {
		t.Errorf("got tenant=%q role=%q, want tenant-acme and empty role", got.TenantId, got.TenantRole)
	}
}

func TestToBaseUser_CopiesTenantFields(t *testing.T) {
	b := (&User{Id: 1, TenantId: "t9", TenantRole: entity.TenantRoleDeptLead}).ToBaseUser()
	if b.TenantId != "t9" || b.TenantRole != entity.TenantRoleDeptLead {
		t.Fatalf("ToBaseUser dropped tenant fields: tenant=%q role=%q", b.TenantId, b.TenantRole)
	}
}
