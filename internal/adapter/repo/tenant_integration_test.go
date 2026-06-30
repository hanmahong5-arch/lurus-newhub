package repo

import (
	"testing"
	"time"
)

// ============================================================================
// Tenant CRUD tests
// ============================================================================

func TestTenant_Create_Success(t *testing.T) {
	SetupTestDB(t)

	tenant := &Tenant{
		Id:           "t-create-001",
		IDPOrgID: "zorg-create-001",
		Slug:         "create-test",
		Name:         "Create Test Tenant",
		Status:       TenantStatusEnabled,
		PlanType:     TenantPlanFree,
		MaxUsers:     50,
		MaxQuota:     500000,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	err := DB.Create(tenant).Error
	if err != nil {
		t.Fatalf("failed to create tenant: %v", err)
	}

	// Verify by fetching
	var fetched Tenant
	err = DB.Where("id = ?", "t-create-001").First(&fetched).Error
	if err != nil {
		t.Fatalf("failed to fetch tenant: %v", err)
	}
	if fetched.Slug != "create-test" {
		t.Errorf("Slug mismatch: got %q", fetched.Slug)
	}
	if fetched.Name != "Create Test Tenant" {
		t.Errorf("Name mismatch: got %q", fetched.Name)
	}
	if fetched.Status != TenantStatusEnabled {
		t.Errorf("Status mismatch: got %d", fetched.Status)
	}
	if fetched.MaxUsers != 50 {
		t.Errorf("MaxUsers mismatch: got %d", fetched.MaxUsers)
	}
}

func TestTenant_Create_DuplicateSlug(t *testing.T) {
	SetupTestDB(t)

	t1 := &Tenant{
		Id: "t-dup-1", IDPOrgID: "zorg-dup-1", Slug: "dup-slug",
		Name: "First", Status: TenantStatusEnabled, PlanType: TenantPlanFree,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	t2 := &Tenant{
		Id: "t-dup-2", IDPOrgID: "zorg-dup-2", Slug: "dup-slug",
		Name: "Second", Status: TenantStatusEnabled, PlanType: TenantPlanFree,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}

	if err := DB.Create(t1).Error; err != nil {
		t.Fatalf("failed to create first tenant: %v", err)
	}
	err := DB.Create(t2).Error
	if err == nil {
		t.Fatal("expected error for duplicate slug, got nil")
	}
}

func TestTenant_GetBySlug(t *testing.T) {
	SetupTestDB(t)

	tenant := &Tenant{
		Id: "t-slug-001", IDPOrgID: "zorg-slug-001", Slug: "slug-lookup",
		Name: "Slug Lookup", Status: TenantStatusEnabled, PlanType: TenantPlanPro,
		MaxUsers: 200, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := DB.Create(tenant).Error; err != nil {
		t.Fatalf("failed to create tenant: %v", err)
	}

	fetched, err := GetTenantBySlug("slug-lookup")
	if err != nil {
		t.Fatalf("GetTenantBySlug() returned error: %v", err)
	}
	if fetched.Id != "t-slug-001" {
		t.Errorf("Id mismatch: got %q", fetched.Id)
	}
	if fetched.PlanType != TenantPlanPro {
		t.Errorf("PlanType mismatch: got %q", fetched.PlanType)
	}
}

func TestTenant_GetBySlug_NotFound(t *testing.T) {
	SetupTestDB(t)

	_, err := GetTenantBySlug("nonexistent-slug")
	if err == nil {
		t.Fatal("expected error for nonexistent slug")
	}
}

func TestTenant_GetByZitadelOrgId(t *testing.T) {
	SetupTestDB(t)

	tenant := &Tenant{
		Id: "t-zorg-001", IDPOrgID: "zitadel-org-abc", Slug: "zorg-lookup",
		Name: "Zitadel Org Lookup", Status: TenantStatusEnabled, PlanType: TenantPlanFree,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := DB.Create(tenant).Error; err != nil {
		t.Fatalf("failed to create tenant: %v", err)
	}

	fetched, err := GetTenantByIDPOrgID("zitadel-org-abc")
	if err != nil {
		t.Fatalf("GetTenantByIDPOrgID() returned error: %v", err)
	}
	if fetched.Id != "t-zorg-001" {
		t.Errorf("Id mismatch: got %q", fetched.Id)
	}
}

func TestTenant_GetByZitadelOrgId_NotFound(t *testing.T) {
	SetupTestDB(t)

	_, err := GetTenantByIDPOrgID("nonexistent-org")
	if err == nil {
		t.Fatal("expected error for nonexistent org ID")
	}
}

func TestTenant_CreateFromZitadel(t *testing.T) {
	SetupTestDB(t)

	tenant, err := CreateTenantFromIDP("zitadel-org-new", "neworg.example.com", "New Org")
	if err != nil {
		t.Fatalf("CreateTenantFromIDP() returned error: %v", err)
	}
	if tenant.IDPOrgID != "zitadel-org-new" {
		t.Errorf("ZitadelOrgID mismatch: got %q", tenant.IDPOrgID)
	}
	if tenant.Slug != "neworg.example.com" {
		t.Errorf("Slug mismatch: got %q", tenant.Slug)
	}
	if tenant.Name != "New Org" {
		t.Errorf("Name mismatch: got %q", tenant.Name)
	}
	if tenant.Status != TenantStatusEnabled {
		t.Errorf("expected status %d, got %d", TenantStatusEnabled, tenant.Status)
	}
	if tenant.PlanType != TenantPlanFree {
		t.Errorf("expected plan %q, got %q", TenantPlanFree, tenant.PlanType)
	}
}

func TestTenant_CreateFromZitadel_Idempotent(t *testing.T) {
	SetupTestDB(t)

	t1, err := CreateTenantFromIDP("zitadel-org-idem", "idem.example.com", "Idem Org")
	if err != nil {
		t.Fatalf("first CreateTenantFromIDP() failed: %v", err)
	}

	t2, err := CreateTenantFromIDP("zitadel-org-idem", "idem.example.com", "Idem Org")
	if err != nil {
		t.Fatalf("second CreateTenantFromIDP() failed: %v", err)
	}

	if t1.Id != t2.Id {
		t.Errorf("expected same tenant ID on idempotent call: %q vs %q", t1.Id, t2.Id)
	}
}

func TestTenant_Disable(t *testing.T) {
	SetupTestDB(t)

	tenant := &Tenant{
		Id: "t-disable-001", IDPOrgID: "zorg-dis-001", Slug: "disable-test",
		Name: "Disable Test", Status: TenantStatusEnabled, PlanType: TenantPlanFree,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := DB.Create(tenant).Error; err != nil {
		t.Fatalf("failed to create tenant: %v", err)
	}

	err := DisableTenant("t-disable-001")
	if err != nil {
		t.Fatalf("DisableTenant() returned error: %v", err)
	}

	fetched, err := GetTenantByID("t-disable-001")
	if err != nil {
		t.Fatalf("GetTenantByID() returned error: %v", err)
	}
	if fetched.Status != TenantStatusDisabled {
		t.Errorf("expected status %d, got %d", TenantStatusDisabled, fetched.Status)
	}
	if fetched.IsEnabled() {
		t.Error("expected IsEnabled() to return false after disable")
	}
	if !fetched.IsDisabled() {
		t.Error("expected IsDisabled() to return true after disable")
	}
}

func TestTenant_Enable(t *testing.T) {
	SetupTestDB(t)

	tenant := &Tenant{
		Id: "t-enable-001", IDPOrgID: "zorg-en-001", Slug: "enable-test",
		Name: "Enable Test", Status: TenantStatusDisabled, PlanType: TenantPlanFree,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := DB.Create(tenant).Error; err != nil {
		t.Fatalf("failed to create tenant: %v", err)
	}

	err := EnableTenant("t-enable-001")
	if err != nil {
		t.Fatalf("EnableTenant() returned error: %v", err)
	}

	fetched, err := GetTenantByID("t-enable-001")
	if err != nil {
		t.Fatalf("GetTenantByID() returned error: %v", err)
	}
	if fetched.Status != TenantStatusEnabled {
		t.Errorf("expected status %d, got %d", TenantStatusEnabled, fetched.Status)
	}
	if !fetched.IsEnabled() {
		t.Error("expected IsEnabled() to return true after enable")
	}
}

func TestTenant_Delete(t *testing.T) {
	SetupTestDB(t)

	tenant := &Tenant{
		Id: "t-del-001", IDPOrgID: "zorg-del-001", Slug: "delete-test",
		Name: "Delete Test", Status: TenantStatusEnabled, PlanType: TenantPlanFree,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := DB.Create(tenant).Error; err != nil {
		t.Fatalf("failed to create tenant: %v", err)
	}

	err := DeleteTenant("t-del-001")
	if err != nil {
		t.Fatalf("DeleteTenant() returned error: %v", err)
	}

	// Soft delete: normal query should not find it
	_, err = GetTenantByID("t-del-001")
	if err == nil {
		t.Fatal("expected error after soft delete, got nil")
	}

	// Unscoped query should still find it
	var unscoped Tenant
	err = DB.Unscoped().Where("id = ?", "t-del-001").First(&unscoped).Error
	if err != nil {
		t.Fatalf("expected to find soft-deleted tenant via Unscoped: %v", err)
	}
	if unscoped.DeletedAt.Valid != true {
		t.Error("expected DeletedAt to be set on soft-deleted tenant")
	}
}

func TestTenant_CanAddUser_WithinLimit(t *testing.T) {
	SetupTestDB(t)

	tenant := &Tenant{
		Id: "t-canadd-001", IDPOrgID: "zorg-canadd-001", Slug: "canadd-test",
		Name: "CanAdd Test", Status: TenantStatusEnabled, PlanType: TenantPlanFree,
		MaxUsers: 10, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := DB.Create(tenant).Error; err != nil {
		t.Fatalf("failed to create tenant: %v", err)
	}

	// No users exist for this tenant, so count=0 < MaxUsers=10
	canAdd, err := TenantCanAddUser(tenant)
	if err != nil {
		t.Fatalf("CanAddUser() returned error: %v", err)
	}
	if !canAdd {
		t.Error("expected CanAddUser() to return true when no users exist")
	}
}

// ============================================================================
// TenantConfig tests
// ============================================================================

func TestTenantConfig_SetAndGet(t *testing.T) {
	SetupTestDB(t)

	tenantID := "t-config-001"
	tenant := &Tenant{
		Id: tenantID, IDPOrgID: "zorg-config-001", Slug: "config-test",
		Name: "Config Test", Status: TenantStatusEnabled, PlanType: TenantPlanFree,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := DB.Create(tenant).Error; err != nil {
		t.Fatalf("failed to create tenant: %v", err)
	}

	// Set a string config
	err := SetTenantConfig(tenantID, "app.name", "My App", ConfigTypeString, "Application name", false)
	if err != nil {
		t.Fatalf("SetTenantConfig() returned error: %v", err)
	}

	// Get it back
	val := GetTenantConfigValue(tenantID, "app.name", "default")
	if val != "My App" {
		t.Errorf("expected config value %q, got %q", "My App", val)
	}

	// Get with default for missing key
	val = GetTenantConfigValue(tenantID, "nonexistent.key", "fallback")
	if val != "fallback" {
		t.Errorf("expected fallback for missing key, got %q", val)
	}
}

func TestTenantConfig_SetInt(t *testing.T) {
	SetupTestDB(t)

	tenantID := "t-config-int"
	tenant := &Tenant{
		Id: tenantID, IDPOrgID: "zorg-config-int", Slug: "config-int-test",
		Name: "Config Int", Status: TenantStatusEnabled, PlanType: TenantPlanFree,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	DB.Create(tenant)

	err := SetTenantConfigInt(tenantID, "quota.limit", 5000, "Quota limit")
	if err != nil {
		t.Fatalf("SetTenantConfigInt() error: %v", err)
	}

	val := GetTenantConfigInt(tenantID, "quota.limit", 0)
	if val != 5000 {
		t.Errorf("expected 5000, got %d", val)
	}
}

func TestTenantConfig_SetBool(t *testing.T) {
	SetupTestDB(t)

	tenantID := "t-config-bool"
	tenant := &Tenant{
		Id: tenantID, IDPOrgID: "zorg-config-bool", Slug: "config-bool-test",
		Name: "Config Bool", Status: TenantStatusEnabled, PlanType: TenantPlanFree,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	DB.Create(tenant)

	err := SetTenantConfigBool(tenantID, "features.enabled", true, "Feature toggle")
	if err != nil {
		t.Fatalf("SetTenantConfigBool() error: %v", err)
	}

	val := GetTenantConfigBool(tenantID, "features.enabled", false)
	if !val {
		t.Error("expected true, got false")
	}
}

func TestTenantConfig_Update(t *testing.T) {
	SetupTestDB(t)

	tenantID := "t-config-upd"
	tenant := &Tenant{
		Id: tenantID, IDPOrgID: "zorg-config-upd", Slug: "config-upd-test",
		Name: "Config Update", Status: TenantStatusEnabled, PlanType: TenantPlanFree,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	DB.Create(tenant)

	// Set initial value
	SetTenantConfig(tenantID, "app.version", "1.0", ConfigTypeString, "Version", false)

	// Update by calling Set again (upsert behavior)
	err := SetTenantConfig(tenantID, "app.version", "2.0", ConfigTypeString, "Version", false)
	if err != nil {
		t.Fatalf("SetTenantConfig() update error: %v", err)
	}

	val := GetTenantConfigValue(tenantID, "app.version", "")
	if val != "2.0" {
		t.Errorf("expected updated value %q, got %q", "2.0", val)
	}
}

func TestTenantConfig_InitializeDefaults(t *testing.T) {
	SetupTestDB(t)

	tenantID := "t-config-defaults"
	tenant := &Tenant{
		Id: tenantID, IDPOrgID: "zorg-config-def", Slug: "config-defaults",
		Name: "Defaults", Status: TenantStatusEnabled, PlanType: TenantPlanFree,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	DB.Create(tenant)

	err := InitializeDefaultTenantConfigs(tenantID)
	if err != nil {
		t.Fatalf("InitializeDefaultTenantConfigs() error: %v", err)
	}

	// Verify some default configs exist
	configs, err := ListTenantConfigs(tenantID, true)
	if err != nil {
		t.Fatalf("ListTenantConfigs() error: %v", err)
	}
	if len(configs) == 0 {
		t.Fatal("expected default configs to be created")
	}

	// Check a specific default
	val := GetTenantConfigValue(tenantID, "quota.new_user_quota", "")
	if val != "10000" {
		t.Errorf("expected default quota '10000', got %q", val)
	}
}
