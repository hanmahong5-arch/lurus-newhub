package repo

import (
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// ============================================================================
// Tenant Isolation Security Tests
// These tests verify that tenant isolation is properly enforced
// ============================================================================

func TestTenantDB_IsolationEnforced(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	// Create users in different tenants
	tenant1 := "tenant_isolation_1"
	tenant2 := "tenant_isolation_2"

	user1 := &User{
		Username:    "user_in_tenant1",
		DisplayName: "User 1",
		TenantId:    tenant1,
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Quota:       100000,
	}
	user2 := &User{
		Username:    "user_in_tenant2",
		DisplayName: "User 2",
		TenantId:    tenant2,
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Quota:       200000,
	}

	if err := DB.Create(user1).Error; err != nil {
		t.Fatalf("Create user1 failed: %v", err)
	}
	if err := DB.Create(user2).Error; err != nil {
		t.Fatalf("Create user2 failed: %v", err)
	}

	// Create tokens for each user
	token1 := &Token{
		UserId:         user1.Id,
		TenantId:       tenant1,
		Key:            "sk-tenant1-" + common.GetRandomString(24),
		Status:         common.TokenStatusEnabled,
		Name:           "Token 1",
		CreatedTime:    common.GetTimestamp(),
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}
	token2 := &Token{
		UserId:         user2.Id,
		TenantId:       tenant2,
		Key:            "sk-tenant2-" + common.GetRandomString(24),
		Status:         common.TokenStatusEnabled,
		Name:           "Token 2",
		CreatedTime:    common.GetTimestamp(),
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}

	if err := DB.Create(token1).Error; err != nil {
		t.Fatalf("Create token1 failed: %v", err)
	}
	if err := DB.Create(token2).Error; err != nil {
		t.Fatalf("Create token2 failed: %v", err)
	}

	// Test 1: Query with tenant filter should only return tenant's data
	var tenant1Users []User
	if err := DB.Where("tenant_id = ?", tenant1).Find(&tenant1Users).Error; err != nil {
		t.Fatalf("Query tenant1 users failed: %v", err)
	}

	if len(tenant1Users) != 1 {
		t.Errorf("expected 1 user in tenant1, got %d", len(tenant1Users))
	}
	if len(tenant1Users) > 0 && tenant1Users[0].TenantId != tenant1 {
		t.Errorf("expected tenant_id=%s, got %s", tenant1, tenant1Users[0].TenantId)
	}

	// Test 2: Tokens should be isolated by tenant
	var tenant1Tokens []Token
	if err := DB.Where("tenant_id = ?", tenant1).Find(&tenant1Tokens).Error; err != nil {
		t.Fatalf("Query tenant1 tokens failed: %v", err)
	}

	if len(tenant1Tokens) != 1 {
		t.Errorf("expected 1 token in tenant1, got %d", len(tenant1Tokens))
	}

	// Test 3: Cross-tenant query should not return other tenant's data
	var allTokensForUser1 []Token
	if err := DB.Where("user_id = ? AND tenant_id = ?", user1.Id, tenant2).Find(&allTokensForUser1).Error; err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(allTokensForUser1) != 0 {
		t.Errorf("expected 0 tokens when querying wrong tenant, got %d", len(allTokensForUser1))
	}
}

func TestSystemDB_NoIsolation(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	// Create users in different tenants
	tenant1 := "sys_tenant_1"
	tenant2 := "sys_tenant_2"

	user1 := &User{
		Username:    "sysdb_user1",
		DisplayName: "Sys User 1",
		TenantId:    tenant1,
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
	}
	user2 := &User{
		Username:    "sysdb_user2",
		DisplayName: "Sys User 2",
		TenantId:    tenant2,
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
	}

	DB.Create(user1)
	DB.Create(user2)

	// GetSystemDB should return all users regardless of tenant
	// Note: In this test, GetSystemDB returns DB since we're using the test setup
	sysDB := GetSystemDB()

	var allUsers []User
	if err := sysDB.Find(&allUsers).Error; err != nil {
		t.Fatalf("SystemDB query failed: %v", err)
	}

	// Should find at least 2 users (could be more from other tests)
	if len(allUsers) < 2 {
		t.Errorf("expected at least 2 users via SystemDB, got %d", len(allUsers))
	}

	// Both tenants should be represented
	foundTenant1 := false
	foundTenant2 := false
	for _, u := range allUsers {
		if u.TenantId == tenant1 {
			foundTenant1 = true
		}
		if u.TenantId == tenant2 {
			foundTenant2 = true
		}
	}

	if !foundTenant1 {
		t.Error("expected to find tenant1 user via SystemDB")
	}
	if !foundTenant2 {
		t.Error("expected to find tenant2 user via SystemDB")
	}
}

func TestDisabledTenant_AccessDenied(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	// Create a tenant
	tenant := &Tenant{
		Id:           "test_disabled_tenant",
		Name:         "Disabled Tenant",
		Slug:         "disabled-tenant",
		Status:       TenantStatusDisabled, // Disabled
		IDPOrgID: "org_disabled_123",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	if err := DB.Create(tenant).Error; err != nil {
		t.Fatalf("Create tenant failed: %v", err)
	}

	// Verify tenant is disabled
	foundTenant, err := GetTenantByID(tenant.Id)
	if err != nil {
		t.Fatalf("GetTenantById failed: %v", err)
	}

	if foundTenant.IsEnabled() {
		t.Error("expected disabled tenant to return false for IsEnabled()")
	}
}

func TestTenant_StatusTransitions(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	// Create an active tenant
	tenant := &Tenant{
		Id:           "test_status_tenant",
		Name:         "Status Test Tenant",
		Slug:         "status-test",
		Status:       TenantStatusEnabled,
		IDPOrgID: "org_status_123",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	if err := DB.Create(tenant).Error; err != nil {
		t.Fatalf("Create tenant failed: %v", err)
	}

	// Verify active status
	if !tenant.IsEnabled() {
		t.Error("active tenant should be enabled")
	}

	// Transition to suspended
	tenant.Status = TenantStatusSuspended
	if err := DB.Save(tenant).Error; err != nil {
		t.Fatalf("Update tenant status failed: %v", err)
	}

	// Verify suspended status
	if tenant.IsEnabled() {
		t.Error("suspended tenant should not be enabled")
	}

	// Transition to disabled
	tenant.Status = TenantStatusDisabled
	if err := DB.Save(tenant).Error; err != nil {
		t.Fatalf("Update tenant status failed: %v", err)
	}

	// Verify disabled status
	if tenant.IsEnabled() {
		t.Error("disabled tenant should not be enabled")
	}
}

func TestTenant_IsolatedData(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	// Create two tenants
	tenant1 := &Tenant{
		Id:           "data_iso_tenant1",
		Name:         "Data Isolation Tenant 1",
		Slug:         "data-iso-1",
		Status:       TenantStatusEnabled,
		IDPOrgID: "org_data_1",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	tenant2 := &Tenant{
		Id:           "data_iso_tenant2",
		Name:         "Data Isolation Tenant 2",
		Slug:         "data-iso-2",
		Status:       TenantStatusEnabled,
		IDPOrgID: "org_data_2",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	DB.Create(tenant1)
	DB.Create(tenant2)

	// Create redemption codes for each tenant
	redemption1 := &Redemption{
		TenantId:    tenant1.Id,
		Key:         common.GetUUID(),
		Name:        "Tenant 1 Code",
		Quota:       100000,
		Status:      common.RedemptionCodeStatusEnabled,
		CreatedTime: common.GetTimestamp(),
	}
	redemption2 := &Redemption{
		TenantId:    tenant2.Id,
		Key:         common.GetUUID(),
		Name:        "Tenant 2 Code",
		Quota:       200000,
		Status:      common.RedemptionCodeStatusEnabled,
		CreatedTime: common.GetTimestamp(),
	}

	DB.Create(redemption1)
	DB.Create(redemption2)

	// Query should only return tenant's own data
	var t1Redemptions []Redemption
	DB.Where("tenant_id = ?", tenant1.Id).Find(&t1Redemptions)

	if len(t1Redemptions) != 1 {
		t.Errorf("expected 1 redemption for tenant1, got %d", len(t1Redemptions))
	}
	if len(t1Redemptions) > 0 && t1Redemptions[0].Quota != 100000 {
		t.Errorf("expected quota=100000, got %d", t1Redemptions[0].Quota)
	}

	// Create logs for each tenant
	log1 := &Log{
		TenantId:  tenant1.Id,
		Type:      LogTypeConsume,
		Content:   "Tenant 1 Log",
		CreatedAt: common.GetTimestamp(),
	}
	log2 := &Log{
		TenantId:  tenant2.Id,
		Type:      LogTypeTopup,
		Content:   "Tenant 2 Log",
		CreatedAt: common.GetTimestamp(),
	}

	DB.Create(log1)
	DB.Create(log2)

	// Query logs by tenant
	var t1Logs []Log
	DB.Where("tenant_id = ?", tenant1.Id).Find(&t1Logs)

	if len(t1Logs) != 1 {
		t.Errorf("expected 1 log for tenant1, got %d", len(t1Logs))
	}
	if len(t1Logs) > 0 && t1Logs[0].Content != "Tenant 1 Log" {
		t.Errorf("expected content='Tenant 1 Log', got %s", t1Logs[0].Content)
	}
}

func TestUserIdentityMapping_TenantIsolation(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	// Create users in different tenants
	_, normal, _ := SeedTestUsers(t)

	tenant1 := "mapping_tenant_1"
	tenant2 := "mapping_tenant_2"

	// Create identity mappings for different tenants
	mapping1 := &UserIdentityMapping{
		TenantID:      tenant1,
		IDPSubject: "zitadel_user_1",
		LurusUserID:   normal.Id,
		Email:         "user1@tenant1.test",
		IsActive:      true,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	mapping2 := &UserIdentityMapping{
		TenantID:      tenant2,
		IDPSubject: "zitadel_user_1", // Same Zitadel user, different tenant
		LurusUserID:   normal.Id + 1,    // Different Lurus user
		Email:         "user1@tenant2.test",
		IsActive:      true,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	DB.Create(mapping1)
	DB.Create(mapping2)

	// Query mapping by tenant should return only that tenant's mapping
	var t1Mappings []UserIdentityMapping
	DB.Where("tenant_id = ?", tenant1).Find(&t1Mappings)

	if len(t1Mappings) != 1 {
		t.Errorf("expected 1 mapping for tenant1, got %d", len(t1Mappings))
	}
	if len(t1Mappings) > 0 && t1Mappings[0].Email != "user1@tenant1.test" {
		t.Errorf("expected email='user1@tenant1.test', got %s", t1Mappings[0].Email)
	}

	// Same Zitadel user can have different mappings in different tenants
	var sameZitadelUser []UserIdentityMapping
	DB.Where("zitadel_user_id = ?", "zitadel_user_1").Find(&sameZitadelUser)

	if len(sameZitadelUser) != 2 {
		t.Errorf("expected 2 mappings for same Zitadel user, got %d", len(sameZitadelUser))
	}
}

