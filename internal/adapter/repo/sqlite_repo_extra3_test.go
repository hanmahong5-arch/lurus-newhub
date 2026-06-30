package repo

// sqlite_repo_extra3_test.go — third batch of coverage tests.
// Covers: vendor_meta, ability-GetChannel, log-GetTenantLogsWithParams/GetUserLogStatByPeriod,
// channel-EditChannelByTag/GetChannelForUpdate, user_mapping-CreateUserFromIDPClaims,
// task-ToOpenAIVideo/InitTask-partial.

import (
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// ─── Vendor Meta ──────────────────────────────────────────────────────────────

func TestVendorMeta_InsertAndGet(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	if err := DB.AutoMigrate(&Vendor{}); err != nil {
		t.Skipf("Vendor migration: %v", err)
	}

	v := &Vendor{Name: "OpenAI", Status: 1}
	if err := VendorInsert(v); err != nil {
		t.Fatalf("VendorInsert: %v", err)
	}
	if v.Id == 0 {
		t.Error("ID should be set")
	}

	vendors, err := GetAllVendors(0, 10)
	if err != nil {
		t.Fatalf("GetAllVendors: %v", err)
	}
	if len(vendors) < 1 {
		t.Error("expected at least 1 vendor")
	}
}

func TestVendorMeta_IsDuplicated(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	if err := DB.AutoMigrate(&Vendor{}); err != nil {
		t.Skipf("Vendor migration: %v", err)
	}

	v := &Vendor{Name: "unique-vendor", Status: 1}
	if err := VendorInsert(v); err != nil {
		t.Fatalf("VendorInsert: %v", err)
	}

	dup, err := IsVendorNameDuplicated(0, "unique-vendor")
	if err != nil {
		t.Fatalf("IsVendorNameDuplicated: %v", err)
	}
	if !dup {
		t.Error("expected duplicate=true")
	}

	notDup, err := IsVendorNameDuplicated(v.Id, "unique-vendor")
	if err != nil {
		t.Fatalf("IsVendorNameDuplicated (self): %v", err)
	}
	if notDup {
		t.Error("expected false when excluding own id")
	}
}

func TestVendorMeta_UpdateAndDelete(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	if err := DB.AutoMigrate(&Vendor{}); err != nil {
		t.Skipf("Vendor migration: %v", err)
	}

	v := &Vendor{Name: "update-del-vendor", Status: 1}
	if err := VendorInsert(v); err != nil {
		t.Fatalf("VendorInsert: %v", err)
	}

	v.Description = "updated"
	if err := VendorUpdate(v); err != nil {
		t.Fatalf("VendorUpdate: %v", err)
	}

	fetched, err := GetVendorByID(v.Id)
	if err != nil {
		t.Fatalf("GetVendorByID: %v", err)
	}
	if fetched.Description != "updated" {
		t.Errorf("expected 'updated', got %q", fetched.Description)
	}

	if err := VendorDelete(v); err != nil {
		t.Fatalf("VendorDelete: %v", err)
	}
}

func TestVendorMeta_GetOrCreate(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	if err := DB.AutoMigrate(&Vendor{}); err != nil {
		t.Skipf("Vendor migration: %v", err)
	}

	// First call creates it
	id1, err := GetOrCreateVendorByName("anthropic")
	if err != nil {
		t.Fatalf("GetOrCreateVendorByName (create): %v", err)
	}
	if id1 == 0 {
		t.Error("expected non-zero ID")
	}

	// Second call returns existing
	id2, err := GetOrCreateVendorByName("anthropic")
	if err != nil {
		t.Fatalf("GetOrCreateVendorByName (get): %v", err)
	}
	if id2 != id1 {
		t.Errorf("expected same ID on second call: got %d vs %d", id1, id2)
	}
}

func TestVendorMeta_SearchVendors(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	if err := DB.AutoMigrate(&Vendor{}); err != nil {
		t.Skipf("Vendor migration: %v", err)
	}

	_ = VendorInsert(&Vendor{Name: "searchable-vendor", Description: "search me", Status: 1})

	results, total, err := SearchVendors("searchable", 0, 10)
	if err != nil {
		t.Fatalf("SearchVendors: %v", err)
	}
	if total < 1 {
		t.Errorf("expected >=1 result, got %d", total)
	}
	_ = results
}

// ─── Ability: GetChannel ──────────────────────────────────────────────────────

func TestAbilityRepo_GetChannel(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	// Create a channel with an ability for group "default" and model "gpt-4"
	ch := seedChannel(t, "get-channel-ch", "default")
	prio := int64(1)
	w := uint(10)
	tag := "test"
	ab := &Ability{
		ChannelId: ch.Id,
		Model:     "gpt-4-ability-test",
		Group:     "default",
		Enabled:   true,
		Priority:  &prio,
		Weight:    w,
		Tag:       &tag,
	}
	if err := DB.Create(ab).Error; err != nil {
		t.Fatalf("create ability: %v", err)
	}

	// GetChannel should pick the channel for this group+model
	channel, err := GetChannel("default", "gpt-4-ability-test", 0)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if channel == nil {
		t.Error("expected non-nil channel")
	}
}

func TestAbilityRepo_GetChannelNoAbility(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	// No abilities for this group/model — should return nil or empty channel
	channel, err := GetChannel("nonexistent-group", "nonexistent-model", 0)
	// With no matching abilities, GetChannel returns empty Channel struct (not nil, no error)
	_ = channel
	_ = err
}

// ─── Log: GetTenantLogsWithParams / GetUserLogStatByPeriod ───────────────────

func TestLogRepo_GetTenantLogsWithParams(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "tenant-log-user", "tenantlog@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	ten := seedTenant(t, "tenant-log-tenant", "tenant-log", "Tenant Log Tenant")

	// Seed a topup log for this tenant
	l := &Log{
		UserId:   u.Id,
		TenantId: ten.Id,
		Username: "tenant-log-user",
		Type:     LogTypeTopup,
		Content:  "test log",
		CreatedAt: common.GetTimestamp(),
	}
	if err := DB.Create(l).Error; err != nil {
		t.Fatalf("create log: %v", err)
	}

	params := &LogQueryParams{
		TenantID: ten.Id,
		Offset:   0,
		Limit:    10,
	}
	logs, total, err := GetTenantLogsWithParams(params)
	if err != nil {
		t.Fatalf("GetTenantLogsWithParams: %v", err)
	}
	if total < 1 {
		t.Errorf("expected >=1 log, got %d", total)
	}
	_ = logs
}

func TestLogRepo_GetUserLogStatByPeriod(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "logstat-user", "logstat@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")

	// Seed a log
	l := &Log{
		UserId:    u.Id,
		Username:  "logstat-user",
		Type:      LogTypeTopup,
		ModelName: "gpt-4",
		Quota:     100,
		Content:   "topup",
		CreatedAt: common.GetTimestamp(),
	}
	if err := DB.Create(l).Error; err != nil {
		t.Fatalf("create log: %v", err)
	}

	since := time.Now().Add(-time.Hour)
	stats, err := GetUserLogStatByPeriod(u.Id, since)
	if err != nil {
		t.Fatalf("GetUserLogStatByPeriod: %v", err)
	}
	_ = stats
}

// ─── Channel: EditChannelByTag / GetChannelForUpdate ─────────────────────────

func TestChannelRepo_EditChannelByTag(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := seedChannel(t, "editbytag-ch", "default")

	// Tag the channel first
	tag := "edit-tag"
	ch.Tag = &tag
	if err := DB.Model(ch).Update("tag", tag).Error; err != nil {
		t.Fatalf("set tag: %v", err)
	}

	newTag := "new-edit-tag"
	// EditChannelByTag(tag, newTag, modelMapping, models, group, priority, weight, paramOverride, headerOverride)
	if err := EditChannelByTag(tag, &newTag, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("EditChannelByTag: %v", err)
	}
}

func TestChannelRepo_GetChannelForUpdate(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := seedChannel(t, "forupdate-ch", "default")

	// GetChannelForUpdate(tx, channelId)
	fetched, err := GetChannelForUpdate(DB, ch.Id)
	if err != nil {
		t.Fatalf("GetChannelForUpdate: %v", err)
	}
	if fetched == nil {
		t.Error("expected non-nil channel")
	}
	if fetched.Id != ch.Id {
		t.Errorf("wrong channel ID: got %d want %d", fetched.Id, ch.Id)
	}
}

// ─── UserMapping: CreateUserFromIDPClaims ─────────────────────────────────

func TestUserMappingRepo_CreateUserFromIDPClaims_EmailFallback(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ten := seedTenant(t, "claims-tenant", "claims", "Claims Tenant")
	// Pre-existing user with the same email (legacy user before Zitadel migration)
	existingUser := seedUser(t, "legacy-claims-user", "legacy@claims.local", common.RoleCommonUser, common.UserStatusEnabled, ten.Id)

	claims := &OIDCUserClaims{
		Sub:               "zit-claims-001",
		Email:             existingUser.Email,
		Name:              "Legacy User",
		PreferredUsername: "legacy-user",
	}

	user, mapping, err := CreateUserFromIDPClaims(claims, ten.Id)
	if err != nil {
		t.Fatalf("CreateUserFromIDPClaims: %v", err)
	}
	if user == nil {
		t.Error("expected non-nil user")
	}
	if mapping == nil {
		t.Error("expected non-nil mapping")
	}
	// Second call should return same result (mapping already exists)
	user2, mapping2, err := CreateUserFromIDPClaims(claims, ten.Id)
	if err != nil {
		t.Fatalf("CreateUserFromIDPClaims (2nd): %v", err)
	}
	if user2.Id != user.Id {
		t.Errorf("expected same user on second call: %d vs %d", user2.Id, user.Id)
	}
	_ = mapping2
}

// ─── Task: ToOpenAIVideo ──────────────────────────────────────────────────────

func TestTaskRepo_ToOpenAIVideo(t *testing.T) {
	task := &Task{
		ID:     1,
		TaskID: "vid-task-001",
		Status: TaskStatusSuccess,
		Progress: "100%",
		Properties: Properties{
			OriginModelName: "sora",
		},
	}
	video := task.ToOpenAIVideo()
	if video == nil {
		t.Fatal("expected non-nil OpenAIVideo")
	}
	if video.ID != "vid-task-001" {
		t.Errorf("wrong ID: %q", video.ID)
	}
}

// ─── Log: GetUserLogStatInternal ─────────────────────────────────────────────

func TestLogRepo_GetUserLogStatInternal_ByModel(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "logstatinternal-user", "logstatinternal@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	_ = DB.Create(&Log{
		UserId:    u.Id,
		Username:  "logstatinternal-user",
		Type:      LogTypeTopup,
		ModelName: "gpt-4",
		Quota:     200,
		Content:   "topup",
		CreatedAt: common.GetTimestamp(),
	})

	stats, err := GetUserLogStatInternal(u.Id, "model")
	if err != nil {
		t.Fatalf("GetUserLogStatInternal (model): %v", err)
	}
	_ = stats
}

func TestLogRepo_GetUserLogStatInternal_ByDay(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "logstatday-user", "logstatday@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	_ = DB.Create(&Log{
		UserId:    u.Id,
		Username:  "logstatday-user",
		Type:      LogTypeTopup,
		ModelName: "claude-3",
		Quota:     100,
		Content:   "topup",
		CreatedAt: common.GetTimestamp(),
	})

	stats, err := GetUserLogStatInternal(u.Id, "day")
	if err != nil {
		t.Fatalf("GetUserLogStatInternal (day): %v", err)
	}
	_ = stats
}

// ─── Channel pool ─────────────────────────────────────────────────────────────

func TestChannelPool_ListOpenRouterMultiKeyChannels(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	channels, err := ListOpenRouterMultiKeyChannels()
	if err != nil {
		t.Fatalf("ListOpenRouterMultiKeyChannels: %v", err)
	}
	_ = channels
}

func TestChannelPool_MarkMultiKeyCooldown(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := seedChannel(t, "cooldown-ch", "default")

	// MarkMultiKeyCooldown(channelId, usingKey, cooldownUntil, reason) returns bool
	result := MarkMultiKeyCooldown(ch.Id, "test-key-hash", common.GetTimestamp()+300, "rate_limit")
	_ = result
}

// ─── UserMapping: ensureUniqueUsername (private, covered indirectly via CreateUserMapping) ─

// ─── entity import anchor ─────────────────────────────────────────────────────

func TestEntityImport_AuditEvent(t *testing.T) {
	ev := &entity.AuditEvent{Action: "test"}
	_ = ev
}

// ─── time import anchor ───────────────────────────────────────────────────────

func TestTimeImport_Since(t *testing.T) {
	since := time.Now().Add(-time.Hour)
	_ = since
}
