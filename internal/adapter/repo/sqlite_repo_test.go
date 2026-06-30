package repo

// sqlite_repo_test.go — integration-style coverage tests for the four highest-gap
// repo packages: tenant, channel, log, redemption, token, user.
//
// All tests run against an in-memory SQLite DB via setupSQLiteDB; no external
// services are required and go test -short picks them up (no testing.Short guard).

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// ─── Tenant ──────────────────────────────────────────────────────────────────

func TestTenantRepo_GetBySlug(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ten := seedTenant(t, "tenant-abc", "acme", "Acme Corp")

	got, err := GetTenantBySlug("acme")
	if err != nil {
		t.Fatalf("GetTenantBySlug: %v", err)
	}
	if got.Id != ten.Id {
		t.Errorf("got tenant id %q, want %q", got.Id, ten.Id)
	}

	_, err = GetTenantBySlug("no-such-slug")
	if err == nil {
		t.Error("expected error for non-existent slug, got nil")
	}
}

func TestTenantRepo_GetByID(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ten := seedTenant(t, "tenant-get-id", "getid", "Get ID Tenant")

	got, err := GetTenantByID(ten.Id)
	if err != nil {
		t.Fatalf("GetTenantByID: %v", err)
	}
	if got.Slug != ten.Slug {
		t.Errorf("got slug %q, want %q", got.Slug, ten.Slug)
	}

	_, err = GetTenantByID("no-such-id")
	if err == nil {
		t.Error("expected error for non-existent tenant id, got nil")
	}
}

func TestTenantRepo_ListTenants(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	for i := 0; i < 5; i++ {
		seedTenant(t, fmt.Sprintf("lt%d", i), fmt.Sprintf("ltslug%d", i), fmt.Sprintf("List Tenant %d", i))
	}

	tenants, total, err := ListTenants(0, 3, 0)
	if err != nil {
		t.Fatalf("ListTenants: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(tenants) != 3 {
		t.Errorf("page len = %d, want 3", len(tenants))
	}

	// Second page
	tenants2, _, err := ListTenants(3, 3, 0)
	if err != nil {
		t.Fatalf("ListTenants page2: %v", err)
	}
	if len(tenants2) != 2 {
		t.Errorf("page2 len = %d, want 2", len(tenants2))
	}
}

func TestTenantRepo_DisableAndEnable(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ten := seedTenant(t, "tenant-status", "status-slug", "Status Tenant")

	if err := DisableTenant(ten.Id); err != nil {
		t.Fatalf("DisableTenant: %v", err)
	}

	got, err := GetTenantByID(ten.Id)
	if err != nil {
		t.Fatalf("GetTenantByID after disable: %v", err)
	}
	if got.Status != TenantStatusDisabled {
		t.Errorf("status = %d, want %d (disabled)", got.Status, TenantStatusDisabled)
	}

	if err := EnableTenant(ten.Id); err != nil {
		t.Fatalf("EnableTenant: %v", err)
	}

	got2, err := GetTenantByID(ten.Id)
	if err != nil {
		t.Fatalf("GetTenantByID after enable: %v", err)
	}
	if got2.Status != TenantStatusEnabled {
		t.Errorf("status = %d, want %d (enabled)", got2.Status, TenantStatusEnabled)
	}
}

func TestTenantRepo_GetByZitadelOrgID(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ten := &Tenant{
		Id:           "tenant-zid",
		Slug:         "zidslug",
		Name:         "Zitadel Tenant",
		Status:       TenantStatusEnabled,
		IDPOrgID: "org_abc123",
	}
	DB.Create(ten)

	got, err := GetTenantByIDPOrgID("org_abc123")
	if err != nil {
		t.Fatalf("GetTenantByIDPOrgID: %v", err)
	}
	if got.Id != ten.Id {
		t.Errorf("got id %q, want %q", got.Id, ten.Id)
	}

	_, err = GetTenantByIDPOrgID("no-such-org")
	if err == nil {
		t.Error("expected error for missing org ID, got nil")
	}
}

func TestTenantRepo_UpdateTenant(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ten := seedTenant(t, "tenant-upd", "updslug", "Update Tenant")

	if err := UpdateTenant(ten.Id, map[string]interface{}{"name": "Updated Name"}); err != nil {
		t.Fatalf("UpdateTenant: %v", err)
	}

	got, err := GetTenantByID(ten.Id)
	if err != nil {
		t.Fatalf("GetTenantByID after update: %v", err)
	}
	if got.Name != "Updated Name" {
		t.Errorf("name = %q, want Updated Name", got.Name)
	}
}

func TestTenantRepo_DeleteTenant(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ten := seedTenant(t, "tenant-del", "delslug", "Delete Tenant")

	if err := DeleteTenant(ten.Id); err != nil {
		t.Fatalf("DeleteTenant: %v", err)
	}

	_, err := GetTenantByID(ten.Id)
	if err == nil {
		t.Error("expected error after soft delete, got nil")
	}
}

func TestTenantRepo_GetTenantCounts(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	// Seed token and channel for a tenant.
	user := seedUser(t, "cnt-user", "cnt@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-cnt")

	for i := 0; i < 3; i++ {
		tok := &Token{
			UserId:         user.Id,
			TenantId:       "tenant-cnt",
			Key:            common.GetRandomString(48),
			Name:           fmt.Sprintf("tok-%d", i),
			Status:         common.TokenStatusEnabled,
			CreatedTime:    common.GetTimestamp(),
			AccessedTime:   common.GetTimestamp(),
			ExpiredTime:    -1,
			UnlimitedQuota: true,
			Group:          "default",
		}
		DB.Create(tok)
	}

	tokenCount, err := GetTenantTokenCount("tenant-cnt")
	if err != nil {
		t.Fatalf("GetTenantTokenCount: %v", err)
	}
	if tokenCount != 3 {
		t.Errorf("token count = %d, want 3", tokenCount)
	}

	channelCount, err := GetTenantChannelCount("tenant-cnt")
	if err != nil {
		t.Fatalf("GetTenantChannelCount: %v", err)
	}
	// No channels seeded, should be 0.
	if channelCount != 0 {
		t.Errorf("channel count = %d, want 0", channelCount)
	}
}

// ─── Redemption ──────────────────────────────────────────────────────────────

func TestRedemptionRepo_InsertAndGet(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	user := seedUser(t, "redeem-user", "redeem@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-r1")

	r := seedRedemptionRow(t, user.Id, "tenant-r1", "Test Code", 50000)

	got, err := GetRedemptionById(r.Id)
	if err != nil {
		t.Fatalf("GetRedemptionById: %v", err)
	}
	if got.Quota != 50000 {
		t.Errorf("quota = %d, want 50000", got.Quota)
	}
	if got.TenantId != "tenant-r1" {
		t.Errorf("tenant_id = %q, want %q", got.TenantId, "tenant-r1")
	}
}

func TestRedemptionRepo_GetById_NotFound(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	_, err := GetRedemptionById(999999)
	if err == nil {
		t.Error("expected error for non-existent ID, got nil")
	}

	_, err = GetRedemptionById(0)
	if err == nil {
		t.Error("expected error for id=0, got nil")
	}
}

func TestRedemptionRepo_GetByTenant(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	user := seedUser(t, "rt-user", "rt@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-rt")

	// Seed 5 for tenant-rt, 3 for other-tenant.
	for i := 0; i < 5; i++ {
		seedRedemptionRow(t, user.Id, "tenant-rt", fmt.Sprintf("own-%d", i), 1000)
	}
	for i := 0; i < 3; i++ {
		seedRedemptionRow(t, user.Id, "other-tenant", fmt.Sprintf("other-%d", i), 1000)
	}

	rows, total, err := GetRedemptionsByTenant("tenant-rt", 0, 10)
	if err != nil {
		t.Fatalf("GetRedemptionsByTenant: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(rows) != 5 {
		t.Errorf("rows = %d, want 5", len(rows))
	}
	for _, r := range rows {
		if r.TenantId != "tenant-rt" {
			t.Errorf("cross-tenant row leaked: tenant_id = %q", r.TenantId)
		}
	}
}

func TestRedemptionRepo_SearchByTenant(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	user := seedUser(t, "search-user", "search@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-s")

	seedRedemptionRow(t, user.Id, "tenant-s", "Alpha Code", 1000)
	seedRedemptionRow(t, user.Id, "tenant-s", "Beta Code", 1000)
	seedRedemptionRow(t, user.Id, "tenant-s", "Alpha Plus", 1000)
	// Different tenant — must not appear in search results.
	seedRedemptionRow(t, user.Id, "other-tenant", "Alpha Other", 1000)

	rows, total, err := SearchRedemptionsByTenant("tenant-s", "Alpha", 0, 10)
	if err != nil {
		t.Fatalf("SearchRedemptionsByTenant: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2 (only own-tenant Alpha* codes)", total)
	}
	if len(rows) != 2 {
		t.Errorf("rows = %d, want 2", len(rows))
	}
	for _, r := range rows {
		if r.TenantId != "tenant-s" {
			t.Errorf("cross-tenant row leaked: tenant_id = %q", r.TenantId)
		}
	}
}

func TestRedemptionRepo_Delete(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	user := seedUser(t, "del-user", "del@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-d")
	r := seedRedemptionRow(t, user.Id, "tenant-d", "Delete Me", 1000)

	if err := RedemptionDelete(r); err != nil {
		t.Fatalf("RedemptionDelete: %v", err)
	}

	_, err := GetRedemptionById(r.Id)
	if err == nil {
		t.Error("expected error after soft-delete, got nil")
	}
}

func TestRedemptionRepo_Update(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	user := seedUser(t, "upd-user", "upd@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-u")
	r := seedRedemptionRow(t, user.Id, "tenant-u", "Update Me", 1000)

	r.Name = "Updated Name"
	r.Quota = 2000
	if err := RedemptionUpdate(r); err != nil {
		t.Fatalf("RedemptionUpdate: %v", err)
	}

	got, err := GetRedemptionById(r.Id)
	if err != nil {
		t.Fatalf("GetRedemptionById after update: %v", err)
	}
	if got.Name != "Updated Name" {
		t.Errorf("name = %q, want %q", got.Name, "Updated Name")
	}
	if got.Quota != 2000 {
		t.Errorf("quota = %d, want 2000", got.Quota)
	}
}

func TestRedemptionRepo_DeleteById(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	user := seedUser(t, "delid-user", "delid@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-delid")
	r := seedRedemptionRow(t, user.Id, "tenant-delid", "Delete By ID", 1000)

	if err := DeleteRedemptionById(r.Id); err != nil {
		t.Fatalf("DeleteRedemptionById: %v", err)
	}

	// Zero id must error.
	if err := DeleteRedemptionById(0); err == nil {
		t.Error("expected error for id=0, got nil")
	}
}

// ─── Token (tenant-scoped) ───────────────────────────────────────────────────

func TestTokenRepo_GetUserTokensByTenant(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	user := seedUser(t, "tok-user", "tok@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-tok")

	// Seed 3 tokens for tenant-tok, 2 for other tenant (same user_id).
	for i := 0; i < 3; i++ {
		tok := &Token{
			UserId:         user.Id,
			TenantId:       "tenant-tok",
			Key:            common.GetRandomString(48),
			Name:           fmt.Sprintf("own-tok-%d", i),
			Status:         common.TokenStatusEnabled,
			CreatedTime:    common.GetTimestamp(),
			AccessedTime:   common.GetTimestamp(),
			ExpiredTime:    -1,
			UnlimitedQuota: true,
			Group:          "default",
		}
		if err := DB.Create(tok).Error; err != nil {
			t.Fatalf("seed token: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		tok := &Token{
			UserId:         user.Id,
			TenantId:       "other-tenant",
			Key:            common.GetRandomString(48),
			Name:           fmt.Sprintf("other-tok-%d", i),
			Status:         common.TokenStatusEnabled,
			CreatedTime:    common.GetTimestamp(),
			AccessedTime:   common.GetTimestamp(),
			ExpiredTime:    -1,
			UnlimitedQuota: true,
			Group:          "default",
		}
		if err := DB.Create(tok).Error; err != nil {
			t.Fatalf("seed other-tenant token: %v", err)
		}
	}

	tokens, err := GetUserTokensByTenant(user.Id, "tenant-tok", 0, 10)
	if err != nil {
		t.Fatalf("GetUserTokensByTenant: %v", err)
	}
	if len(tokens) != 3 {
		t.Errorf("got %d tokens, want 3", len(tokens))
	}
	for _, tok := range tokens {
		if tok.TenantId != "tenant-tok" {
			t.Errorf("cross-tenant token leaked: tenant_id = %q", tok.TenantId)
		}
	}

	total, err := CountUserTokensByTenant(user.Id, "tenant-tok")
	if err != nil {
		t.Fatalf("CountUserTokensByTenant: %v", err)
	}
	if total != 3 {
		t.Errorf("count = %d, want 3", total)
	}
}

// ─── Log ─────────────────────────────────────────────────────────────────────

func TestLogRepo_RecordAndQuery(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	user := seedUser(t, "log-user", "log@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-log")

	RecordLogWithTenant(user.Id, "tenant-log", LogTypeTopup, "Test topup log entry")

	var logs []*Log
	LOG_DB.Where("user_id = ? AND type = ?", user.Id, LogTypeTopup).Find(&logs)
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	if logs[0].Content != "Test topup log entry" {
		t.Errorf("content = %q, want %q", logs[0].Content, "Test topup log entry")
	}
	if logs[0].TenantId != "tenant-log" {
		t.Errorf("tenant_id = %q, want %q", logs[0].TenantId, "tenant-log")
	}
}

func TestLogRepo_TenantIsolation(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	user := seedUser(t, "log-iso-user", "logiso@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-logi")

	// Use LogTypeTopup (not LogTypeConsume) because LogConsumeEnabled is false
	// in the test setup — RecordLogWithTenant silently no-ops for Consume logs
	// when the flag is off. Topup logs are always written.
	for i := 0; i < 3; i++ {
		RecordLogWithTenant(user.Id, "tenant-logi", LogTypeTopup, fmt.Sprintf("topup logi %d", i))
	}
	for i := 0; i < 2; i++ {
		RecordLogWithTenant(user.Id, "other-tenant-log", LogTypeTopup, fmt.Sprintf("topup other %d", i))
	}

	var count int64
	LOG_DB.Model(&Log{}).Where("user_id = ? AND tenant_id = ?", user.Id, "tenant-logi").Count(&count)
	if count != 3 {
		t.Errorf("tenant_logi count = %d, want 3", count)
	}

	var countOther int64
	LOG_DB.Model(&Log{}).Where("user_id = ? AND tenant_id = ?", user.Id, "other-tenant-log").Count(&countOther)
	if countOther != 2 {
		t.Errorf("other-tenant-log count = %d, want 2", countOther)
	}
}

func TestLogRepo_GetTenantRedemptionCount(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	user := seedUser(t, "rcnt-user", "rcnt@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-rcnt")

	for i := 0; i < 4; i++ {
		seedRedemptionRow(t, user.Id, "tenant-rcnt", fmt.Sprintf("r%d", i), 1000)
	}
	// Cross-tenant — must not count.
	seedRedemptionRow(t, user.Id, "other-rcnt", "r-other", 1000)

	count, err := GetTenantRedemptionCount("tenant-rcnt")
	if err != nil {
		t.Fatalf("GetTenantRedemptionCount: %v", err)
	}
	if count != 4 {
		t.Errorf("redemption count = %d, want 4", count)
	}
}

// ─── Channel ─────────────────────────────────────────────────────────────────

func TestChannelRepo_GetByTenant(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	// Seed 4 channels in tenant-ch, 2 in other-tenant.
	for i := 0; i < 4; i++ {
		ch := &Channel{
			Name:        fmt.Sprintf("ch-%d", i),
			TenantId:    "tenant-ch",
			Type:        1,
			Key:         "sk-test-" + common.GetRandomString(24),
			Status:      common.ChannelStatusEnabled,
			Models:      "gpt-4",
			Group:       "default",
			CreatedTime: common.GetTimestamp(),
		}
		if err := DB.Create(ch).Error; err != nil {
			t.Fatalf("seed channel %d: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		ch := &Channel{
			Name:        fmt.Sprintf("other-ch-%d", i),
			TenantId:    "other-tenant-ch",
			Type:        1,
			Key:         "sk-test-" + common.GetRandomString(24),
			Status:      common.ChannelStatusEnabled,
			Models:      "gpt-4",
			Group:       "default",
			CreatedTime: common.GetTimestamp(),
		}
		if err := DB.Create(ch).Error; err != nil {
			t.Fatalf("seed other channel %d: %v", i, err)
		}
	}

	channels, err := GetChannelsByTenant("tenant-ch", 0, 10, false)
	if err != nil {
		t.Fatalf("GetChannelsByTenant: %v", err)
	}
	if len(channels) != 4 {
		t.Errorf("channels = %d, want 4", len(channels))
	}
	for _, ch := range channels {
		if ch.TenantId != "tenant-ch" {
			t.Errorf("cross-tenant channel leaked: tenant_id = %q", ch.TenantId)
		}
	}
}

func TestChannelRepo_SearchByTenant(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	for i := 0; i < 3; i++ {
		ch := &Channel{
			Name:        fmt.Sprintf("openai-ch-%d", i),
			TenantId:    "tenant-search",
			Type:        1,
			Key:         "sk-test-" + common.GetRandomString(24),
			Status:      common.ChannelStatusEnabled,
			Models:      "gpt-4",
			Group:       "default",
			CreatedTime: common.GetTimestamp(),
		}
		DB.Create(ch)
	}
	// Non-matching channel in same tenant.
	DB.Create(&Channel{
		Name:        "anthropic-ch",
		TenantId:    "tenant-search",
		Type:        2,
		Key:         "sk-test-" + common.GetRandomString(24),
		Status:      common.ChannelStatusEnabled,
		Models:      "claude-3",
		Group:       "default",
		CreatedTime: common.GetTimestamp(),
	})
	// Cross-tenant channel — must not appear.
	DB.Create(&Channel{
		Name:        "openai-other",
		TenantId:    "other-tenant-search",
		Type:        1,
		Key:         "sk-test-" + common.GetRandomString(24),
		Status:      common.ChannelStatusEnabled,
		Models:      "gpt-4",
		Group:       "default",
		CreatedTime: common.GetTimestamp(),
	})

	// SearchChannelsByTenant(tenantID, keyword, group, model, idSort)
	channels, err := SearchChannelsByTenant("tenant-search", "openai", "", "", false)
	if err != nil {
		t.Fatalf("SearchChannelsByTenant: %v", err)
	}
	if len(channels) != 3 {
		t.Errorf("channels = %d, want 3", len(channels))
	}
}

func TestChannelRepo_GetByID(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := &Channel{
		Name:        "id-lookup-ch",
		TenantId:    "tenant-idlookup",
		Type:        1,
		Key:         "sk-test-" + common.GetRandomString(24),
		Status:      common.ChannelStatusEnabled,
		Models:      "gpt-4",
		Group:       "default",
		CreatedTime: common.GetTimestamp(),
	}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	got, err := GetChannelById(ch.Id, false)
	if err != nil {
		t.Fatalf("GetChannelById: %v", err)
	}
	if got.Name != "id-lookup-ch" {
		t.Errorf("name = %q, want id-lookup-ch", got.Name)
	}

	_, err = GetChannelById(999999, false)
	if err == nil {
		t.Error("expected error for non-existent channel id, got nil")
	}
}

// ─── User ────────────────────────────────────────────────────────────────────

func TestUserRepo_GetByID(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "getbyid-user", "getbyid@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-uid")

	got, err := GetUserById(u.Id, false)
	if err != nil {
		t.Fatalf("GetUserById: %v", err)
	}
	if got.Username != "getbyid-user" {
		t.Errorf("username = %q, want getbyid-user", got.Username)
	}
}

func TestUserRepo_GetByEmail_FillUser(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "email-user", "uniqueemail@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-email")

	// FillUserByEmail populates the user struct in-place and always returns nil
	// (GORM record-not-found does not surface as an error in this function).
	found := &User{Email: "uniqueemail@test.local"}
	_ = found.FillUserByEmail()
	if found.Id != u.Id {
		t.Errorf("id = %d, want %d", found.Id, u.Id)
	}

	// For a missing email, Id stays 0 (GORM fills nothing; no rows found).
	notFound := &User{Email: "noone@test.local"}
	_ = notFound.FillUserByEmail()
	if notFound.Id != 0 {
		t.Errorf("expected Id=0 for missing email, got %d", notFound.Id)
	}
}

func TestUserRepo_IsEmailAlreadyTaken(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	seedUser(t, "taken-user", "taken@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-taken")

	if !IsEmailAlreadyTaken("taken@test.local") {
		t.Error("expected email to be taken")
	}
	if IsEmailAlreadyTaken("free@test.local") {
		t.Error("expected email to be free")
	}
}

func TestUserRepo_IncreaseDecreaseQuota(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "quota-user", "quota@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-quota")

	initial, err := GetUserQuota(u.Id, true)
	if err != nil {
		t.Fatalf("GetUserQuota initial: %v", err)
	}

	if err := IncreaseUserQuota(u.Id, 50000, true); err != nil {
		t.Fatalf("IncreaseUserQuota: %v", err)
	}

	after, err := GetUserQuota(u.Id, true)
	if err != nil {
		t.Fatalf("GetUserQuota after increase: %v", err)
	}
	if after != initial+50000 {
		t.Errorf("quota after increase = %d, want %d", after, initial+50000)
	}

	if err := DecreaseUserQuota(u.Id, 10000); err != nil {
		t.Fatalf("DecreaseUserQuota: %v", err)
	}

	final, err := GetUserQuota(u.Id, true)
	if err != nil {
		t.Fatalf("GetUserQuota final: %v", err)
	}
	if final != initial+40000 {
		t.Errorf("quota after decrease = %d, want %d", final, initial+40000)
	}
}

// ─── User — extended ─────────────────────────────────────────────────────────

func TestUserRepo_GetAllUsers(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	for i := 0; i < 4; i++ {
		seedUser(t, fmt.Sprintf("allu%d", i), fmt.Sprintf("allu%d@test.local", i), common.RoleCommonUser, common.UserStatusEnabled, "tenant-all")
	}

	page := &common.PageInfo{Page: 1, PageSize: 20}
	users, total, err := GetAllUsers(page)
	if err != nil {
		t.Fatalf("GetAllUsers: %v", err)
	}
	if total < 4 {
		t.Errorf("GetAllUsers total = %d, want >= 4", total)
	}
	if len(users) == 0 {
		t.Error("GetAllUsers returned empty slice")
	}
}

func TestUserRepo_SearchUsers(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	seedUser(t, "searchable-user", "searchable@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-search")
	seedUser(t, "other-user", "other@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-search")

	users, total, err := SearchUsers("searchable", "", 0, 10)
	if err != nil {
		t.Fatalf("SearchUsers: %v", err)
	}
	if total != 1 {
		t.Errorf("SearchUsers total = %d, want 1", total)
	}
	if len(users) != 1 || users[0].Username != "searchable-user" {
		t.Errorf("SearchUsers result mismatch: got %+v", users)
	}
}

func TestUserRepo_DeleteUserById(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "del-user", "del@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-del")

	if err := DeleteUserById(u.Id); err != nil {
		t.Fatalf("DeleteUserById: %v", err)
	}

	// soft-deleted: GetUserById should fail
	_, err := GetUserById(u.Id)
	if err == nil {
		t.Error("expected error after soft-delete, got nil")
	}

	// zero id
	if err := DeleteUserById(0); err == nil {
		t.Error("expected error for id=0, got nil")
	}
}

func TestUserRepo_DisableUserById(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "dis-user", "dis@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-dis")

	if err := DisableUserById(u.Id); err != nil {
		t.Fatalf("DisableUserById: %v", err)
	}
	got, err := GetUserById(u.Id)
	if err != nil {
		t.Fatalf("GetUserById after disable: %v", err)
	}
	if got.Status != common.UserStatusDisabled {
		t.Errorf("user status = %d, want disabled (%d)", got.Status, common.UserStatusDisabled)
	}

	// zero id
	if err := DisableUserById(0); err == nil {
		t.Error("expected error for id=0, got nil")
	}
}

func TestUserRepo_InsertAndUpdate(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := &User{
		Username:    "ins-update-user",
		DisplayName: "Insert Update",
		Email:       "insupdate@test.local",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		TenantId:    "tenant-iu",
		Quota:       100,
	}
	if err := u.Insert(); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if u.Id == 0 {
		t.Fatal("Insert: id is still 0 after insert")
	}

	u.DisplayName = "Updated Name"
	if err := u.Update(); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := GetUserById(u.Id)
	if err != nil {
		t.Fatalf("GetUserById after update: %v", err)
	}
	if got.DisplayName != "Updated Name" {
		t.Errorf("DisplayName = %q, want 'Updated Name'", got.DisplayName)
	}
}

func TestUserRepo_IsAdmin(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	admin := seedUser(t, "admin-check", "admin-check@test.local", common.RoleAdminUser, common.UserStatusEnabled, "tenant-admin")
	regular := seedUser(t, "regular-check", "regular-check@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-admin")

	if !IsAdmin(admin.Id) {
		t.Error("IsAdmin(admin) = false, want true")
	}
	if IsAdmin(regular.Id) {
		t.Error("IsAdmin(regular) = true, want false")
	}
	if IsAdmin(0) {
		t.Error("IsAdmin(0) = true, want false")
	}
}

func TestUserRepo_GetUserRole(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "role-user", "role-user@test.local", common.RoleAdminUser, common.UserStatusEnabled, "tenant-role")

	role, err := GetUserRole(u.Id)
	if err != nil {
		t.Fatalf("GetUserRole: %v", err)
	}
	if role != common.RoleAdminUser {
		t.Errorf("role = %d, want admin (%d)", role, common.RoleAdminUser)
	}
}

func TestUserRepo_HardDeleteUserById(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "hard-del-user", "hd@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-hd")

	if err := HardDeleteUserById(u.Id); err != nil {
		t.Fatalf("HardDeleteUserById: %v", err)
	}

	// Should not be found even with Unscoped
	var found User
	err := DB.Unscoped().First(&found, u.Id).Error
	if err == nil {
		t.Error("expected record-not-found after hard delete, got nil error")
	}

	if err := HardDeleteUserById(0); err == nil {
		t.Error("expected error for id=0, got nil")
	}
}

func TestUserRepo_GetMaxUserId(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u1 := seedUser(t, "maxid-a", "maxid-a@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-mx")
	u2 := seedUser(t, "maxid-b", "maxid-b@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-mx")

	max := GetMaxUserId()
	if max < u1.Id || max < u2.Id {
		t.Errorf("GetMaxUserId = %d, must be >= %d and >= %d", max, u1.Id, u2.Id)
	}
}

func TestUserRepo_ValidateAccessToken(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := &User{
		Username:    "access-token-user",
		DisplayName: "Access Token",
		Email:       "at@test.local",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		TenantId:    "tenant-at",
		Quota:       0,
	}
	if err := u.Insert(); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	accessToken := common.GetRandomString(32)
	u.SetAccessToken(accessToken)
	if err := u.Update(); err != nil {
		t.Fatalf("Update access token: %v", err)
	}

	got := ValidateAccessToken(accessToken)
	if got == nil {
		t.Fatal("ValidateAccessToken returned nil for valid token")
	}
	if got.Id != u.Id {
		t.Errorf("ValidateAccessToken id = %d, want %d", got.Id, u.Id)
	}

	noUser := ValidateAccessToken("no-such-token-xyz")
	if noUser != nil {
		t.Error("ValidateAccessToken should return nil for unknown token")
	}
}

func TestUserRepo_DeltaUpdateUserQuota(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "delta-quota-user", "dq@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-dq")

	before, _ := GetUserQuota(u.Id, true)

	if err := DeltaUpdateUserQuota(u.Id, 999); err != nil {
		t.Fatalf("DeltaUpdateUserQuota: %v", err)
	}

	after, err := GetUserQuota(u.Id, true)
	if err != nil {
		t.Fatalf("GetUserQuota: %v", err)
	}
	if after != before+999 {
		t.Errorf("quota after delta = %d, want %d", after, before+999)
	}
}

// ─── Token — extended ────────────────────────────────────────────────────────

func TestTokenRepo_InsertAndGet(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "tok-insert-user", "tokins@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-tok")

	key := common.GetRandomString(48)
	tok := &Token{
		UserId:        u.Id,
		TenantId:      "tenant-tok",
		Name:          "insert-tok",
		Key:           key,
		Status:        common.TokenStatusEnabled,
		UnlimitedQuota: true,
		RemainQuota:   0,
		ExpiredTime:   -1,
	}
	if err := tok.Insert(); err != nil {
		t.Fatalf("Token.Insert: %v", err)
	}
	if tok.Id == 0 {
		t.Fatal("Token.Insert: id still 0")
	}

	got, err := GetTokenById(tok.Id)
	if err != nil {
		t.Fatalf("GetTokenById: %v", err)
	}
	if got.Name != tok.Name {
		t.Errorf("token name = %q, want %q", got.Name, tok.Name)
	}
}

func TestTokenRepo_GetTokenByIds(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "tok-ids-user", "tokids@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-tokids")

	key := common.GetRandomString(48)
	tok := &Token{UserId: u.Id, TenantId: "tenant-tokids", Name: "ids-tok", Key: key, Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1}
	if err := tok.Insert(); err != nil {
		t.Fatalf("Token.Insert: %v", err)
	}

	got, err := GetTokenByIds(tok.Id, u.Id)
	if err != nil {
		t.Fatalf("GetTokenByIds: %v", err)
	}
	if got.Id != tok.Id {
		t.Errorf("id = %d, want %d", got.Id, tok.Id)
	}

	// Wrong userId
	_, err = GetTokenByIds(tok.Id, u.Id+9999)
	if err == nil {
		t.Error("expected error for wrong userId, got nil")
	}

	// Zero args
	_, err = GetTokenByIds(0, u.Id)
	if err == nil {
		t.Error("expected error for id=0, got nil")
	}
}

func TestTokenRepo_DeleteTokenById(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "tok-del-user", "tokdel@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-tokdel")

	key := common.GetRandomString(48)
	tok := &Token{UserId: u.Id, TenantId: "tenant-tokdel", Name: "del-tok", Key: key, Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1}
	if err := tok.Insert(); err != nil {
		t.Fatalf("Token.Insert: %v", err)
	}

	if err := DeleteTokenById(tok.Id, u.Id); err != nil {
		t.Fatalf("DeleteTokenById: %v", err)
	}
	_, err := GetTokenById(tok.Id)
	if err == nil {
		t.Error("expected error after deletion, got nil")
	}
}

func TestTokenRepo_SearchUserTokens(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "tok-search-user", "toksearch@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-toksearch")

	for _, name := range []string{"alpha-tok", "beta-tok", "alpha-extra"} {
		tok := &Token{UserId: u.Id, TenantId: "tenant-toksearch", Name: name, Key: common.GetRandomString(48), Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1}
		if err := tok.Insert(); err != nil {
			t.Fatalf("Token.Insert %q: %v", name, err)
		}
	}

	toks, err := SearchUserTokens(u.Id, "alpha", "")
	if err != nil {
		t.Fatalf("SearchUserTokens: %v", err)
	}
	if len(toks) != 2 {
		t.Errorf("SearchUserTokens count = %d, want 2", len(toks))
	}
}

func TestTokenRepo_CountUserTokens(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "tok-count-user", "tokcount@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-tokcount")

	for i := 0; i < 3; i++ {
		tok := &Token{UserId: u.Id, TenantId: "tenant-tokcount", Name: fmt.Sprintf("count-tok-%d", i), Key: common.GetRandomString(48), Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1}
		if err := tok.Insert(); err != nil {
			t.Fatalf("Token.Insert: %v", err)
		}
	}

	count, err := CountUserTokens(u.Id)
	if err != nil {
		t.Fatalf("CountUserTokens: %v", err)
	}
	if count != 3 {
		t.Errorf("CountUserTokens = %d, want 3", count)
	}
}

func TestTokenRepo_IncreaseDecreaseTokenQuota(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "tok-quota-user", "tokquota@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-tokquota")

	key := common.GetRandomString(48)
	tok := &Token{UserId: u.Id, TenantId: "tenant-tokquota", Name: "quota-tok", Key: key, Status: common.TokenStatusEnabled, UnlimitedQuota: false, RemainQuota: 1000, ExpiredTime: -1}
	if err := tok.Insert(); err != nil {
		t.Fatalf("Token.Insert: %v", err)
	}

	if err := IncreaseTokenQuota(tok.Id, key, 500); err != nil {
		t.Fatalf("IncreaseTokenQuota: %v", err)
	}
	got, _ := GetTokenById(tok.Id)
	if got.RemainQuota != 1500 {
		t.Errorf("RemainQuota after increase = %d, want 1500", got.RemainQuota)
	}

	if err := DecreaseTokenQuota(tok.Id, key, 200); err != nil {
		t.Fatalf("DecreaseTokenQuota: %v", err)
	}
	got, _ = GetTokenById(tok.Id)
	if got.RemainQuota != 1300 {
		t.Errorf("RemainQuota after decrease = %d, want 1300", got.RemainQuota)
	}
}

func TestTokenRepo_GetIpLimits(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	allowIps := "192.168.1.1\n10.0.0.1\n"
	tok := &Token{AllowIps: &allowIps}
	ips := tok.GetIpLimits()
	if len(ips) != 2 {
		t.Errorf("GetIpLimits len = %d, want 2", len(ips))
	}

	nilTok := &Token{AllowIps: nil}
	if got := nilTok.GetIpLimits(); len(got) != 0 {
		t.Errorf("GetIpLimits nil AllowIps = %v, want empty", got)
	}
}

func TestTokenRepo_ValidateUserToken(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "tok-validate-user", "tokvalidate@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-tokval")

	key := common.GetRandomString(48)
	tok := &Token{UserId: u.Id, TenantId: "tenant-tokval", Name: "validate-tok", Key: key, Status: common.TokenStatusEnabled, UnlimitedQuota: true, RemainQuota: 0, ExpiredTime: -1}
	if err := tok.Insert(); err != nil {
		t.Fatalf("Token.Insert: %v", err)
	}

	valid, err := ValidateUserToken(key)
	if err != nil {
		t.Fatalf("ValidateUserToken: %v", err)
	}
	if valid.Id != tok.Id {
		t.Errorf("ValidateUserToken id = %d, want %d", valid.Id, tok.Id)
	}

	// Empty key
	_, err = ValidateUserToken("")
	if err == nil {
		t.Error("expected error for empty key")
	}

	// Non-existent key
	_, err = ValidateUserToken("sk-totally-nonexistent-key-xyz")
	if err == nil {
		t.Error("expected error for nonexistent key")
	}
}

func TestTokenRepo_AutoCreateDefaultToken(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "auto-tok-user", "autotok@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-autotok")

	tok, err := AutoCreateDefaultToken(u.Id)
	if err != nil {
		t.Fatalf("AutoCreateDefaultToken: %v", err)
	}
	if tok == nil || tok.Id == 0 {
		t.Fatal("AutoCreateDefaultToken returned nil/zero token")
	}
	if tok.Name != "default" {
		t.Errorf("default token name = %q, want 'default'", tok.Name)
	}
}

func TestTokenRepo_GetProvisionedTokensByTenant(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	creator := seedUser(t, "prov-creator", "provcreator@test.local", common.RoleAdminUser, common.UserStatusEnabled, "tenant-prov")

	for i := 0; i < 3; i++ {
		tok := &Token{
			UserId: creator.Id, CreatorUserId: creator.Id, TenantId: "tenant-prov",
			Name: fmt.Sprintf("prov-tok-%d", i), Key: common.GetRandomString(48),
			Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1,
		}
		if err := tok.Insert(); err != nil {
			t.Fatalf("Token.Insert: %v", err)
		}
	}

	toks, err := GetProvisionedTokensByTenant(creator.Id, "tenant-prov", false, 0, 10)
	if err != nil {
		t.Fatalf("GetProvisionedTokensByTenant: %v", err)
	}
	if len(toks) != 3 {
		t.Errorf("GetProvisionedTokensByTenant count = %d, want 3", len(toks))
	}

	count, err := CountProvisionedTokensByTenant(creator.Id, "tenant-prov", false)
	if err != nil {
		t.Fatalf("CountProvisionedTokensByTenant: %v", err)
	}
	if count != 3 {
		t.Errorf("CountProvisionedTokensByTenant = %d, want 3", count)
	}
}

// ─── Channel — extended ──────────────────────────────────────────────────────

func seedChannel(t *testing.T, name, tenantID string) *Channel {
	t.Helper()
	ch := &Channel{
		TenantId:    tenantID,
		Name:        name,
		Type:        1,
		Key:         common.GetRandomString(32),
		Status:      common.ChannelStatusEnabled,
		Models:      "gpt-4,gpt-3.5-turbo",
		Group:       "default",
		CreatedTime: common.GetTimestamp(),
	}
	if err := ch.Insert(); err != nil {
		t.Fatalf("seedChannel %q: %v", name, err)
	}
	return ch
}

func TestChannelRepo_InsertAndSave(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := seedChannel(t, "save-channel", "tenant-save")

	ch.Name = "updated-name"
	if err := ch.Save(); err != nil {
		t.Fatalf("Channel.Save: %v", err)
	}
	got, err := GetChannelById(ch.Id, false)
	if err != nil {
		t.Fatalf("GetChannelById after save: %v", err)
	}
	if got.Name != "updated-name" {
		t.Errorf("name = %q, want 'updated-name'", got.Name)
	}
}

func TestChannelRepo_GetAllChannels(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	for i := 0; i < 4; i++ {
		seedChannel(t, fmt.Sprintf("all-ch-%d", i), "tenant-all-ch")
	}

	all, err := GetAllChannels(0, 100, true, true)
	if err != nil {
		t.Fatalf("GetAllChannels: %v", err)
	}
	if len(all) < 4 {
		t.Errorf("GetAllChannels count = %d, want >= 4", len(all))
	}

	// Pagination without key
	page, err := GetAllChannels(0, 2, false, true)
	if err != nil {
		t.Fatalf("GetAllChannels paginated: %v", err)
	}
	for _, c := range page {
		if c.Key != "" {
			t.Errorf("Key should be omitted in paginated result, got %q", c.Key)
		}
	}
}

func TestChannelRepo_DeleteChannel(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := seedChannel(t, "del-channel", "tenant-del-ch")

	if err := ch.Delete(); err != nil {
		t.Fatalf("Channel.Delete: %v", err)
	}

	_, err := GetChannelById(ch.Id, false)
	if err == nil {
		t.Error("expected error after channel delete, got nil")
	}
}

func TestChannelRepo_UpdateResponseTime(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := seedChannel(t, "rt-channel", "tenant-rt")

	ch.UpdateResponseTime(250)

	got, err := GetChannelById(ch.Id, false)
	if err != nil {
		t.Fatalf("GetChannelById: %v", err)
	}
	if got.ResponseTime != 250 {
		t.Errorf("ResponseTime = %d, want 250", got.ResponseTime)
	}
}

func TestChannelRepo_GetChannelsByTag(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	tag := "test-tag-xyz"
	for i := 0; i < 3; i++ {
		ch := seedChannel(t, fmt.Sprintf("tag-ch-%d", i), "tenant-tag")
		ch.Tag = &tag
		if err := ch.Save(); err != nil {
			t.Fatalf("Save tag: %v", err)
		}
	}
	seedChannel(t, "notag-ch", "tenant-tag") // no tag

	chs, err := GetChannelsByTag(tag, true, false)
	if err != nil {
		t.Fatalf("GetChannelsByTag: %v", err)
	}
	if len(chs) != 3 {
		t.Errorf("GetChannelsByTag count = %d, want 3", len(chs))
	}
}

func TestChannelRepo_GetChannelsByTagAndTenant(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	tag := "isolated-tag"
	myTenant := "tenant-tag-isolated"
	otherTenant := "tenant-tag-other"

	for i := 0; i < 2; i++ {
		ch := seedChannel(t, fmt.Sprintf("my-tag-ch-%d", i), myTenant)
		ch.Tag = &tag
		if err := ch.Save(); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	ch := seedChannel(t, "other-tag-ch", otherTenant)
	ch.Tag = &tag
	if err := ch.Save(); err != nil {
		t.Fatalf("Save other: %v", err)
	}

	chs, err := GetChannelsByTagAndTenant(myTenant, tag, true)
	if err != nil {
		t.Fatalf("GetChannelsByTagAndTenant: %v", err)
	}
	if len(chs) != 2 {
		t.Errorf("count = %d, want 2", len(chs))
	}
	for _, c := range chs {
		if c.TenantId != myTenant {
			t.Errorf("cross-tenant leak: got tenant_id %q", c.TenantId)
		}
	}
}

func TestChannelRepo_CountAllChannels(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	for i := 0; i < 3; i++ {
		seedChannel(t, fmt.Sprintf("cnt-ch-%d", i), "tenant-cnt")
	}

	count, err := CountAllChannels()
	if err != nil {
		t.Fatalf("CountAllChannels: %v", err)
	}
	if count < 3 {
		t.Errorf("CountAllChannels = %d, want >= 3", count)
	}
}

// ─── Log — extended ──────────────────────────────────────────────────────────

func TestLogRepo_GetAllLogs(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "logall-user", "logall@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-logall")
	RecordLog(u.Id, LogTypeTopup, "topup A")
	RecordLog(u.Id, LogTypeTopup, "topup B")
	RecordLog(u.Id, LogTypeSystem, "system msg")

	logs, total, err := GetAllLogs(LogTypeTopup, 0, 0, "", "", "", 0, 10, 0, "")
	if err != nil {
		t.Fatalf("GetAllLogs: %v", err)
	}
	if total < 2 {
		t.Errorf("GetAllLogs total = %d, want >= 2", total)
	}
	_ = logs
}

func TestLogRepo_GetUserLogs(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "logu-user", "logu@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-logu")
	other := seedUser(t, "logu-other", "logu-other@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-logu")

	RecordLog(u.Id, LogTypeTopup, "own topup 1")
	RecordLog(u.Id, LogTypeTopup, "own topup 2")
	RecordLog(other.Id, LogTypeTopup, "other topup")

	logs, total, err := GetUserLogs(u.Id, LogTypeTopup, 0, 0, "", "", 0, 10, "")
	if err != nil {
		t.Fatalf("GetUserLogs: %v", err)
	}
	if total != 2 {
		t.Errorf("GetUserLogs total = %d, want 2", total)
	}
	for _, l := range logs {
		if l.UserId != u.Id {
			t.Errorf("cross-user leak: log user_id = %d, want %d", l.UserId, u.Id)
		}
	}
}

func TestLogRepo_DeleteOldLog(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "logdel-user", "logdel@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-logdel")

	// Insert a log with a very old timestamp directly
	oldLog := &Log{
		UserId:    u.Id,
		Type:      LogTypeSystem,
		Content:   "old log",
		CreatedAt: 1000, // epoch second far in the past
	}
	if err := LOG_DB.Create(oldLog).Error; err != nil {
		t.Fatalf("create old log: %v", err)
	}

	// Delete logs older than now
	cutoff := common.GetTimestamp()
	deleted, err := DeleteOldLog(context.Background(), int64(cutoff), 100)
	if err != nil {
		t.Fatalf("DeleteOldLog: %v", err)
	}
	if deleted < 1 {
		t.Errorf("DeleteOldLog deleted = %d, want >= 1", deleted)
	}
}

func TestLogRepo_SumUsedQuota(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "logsum-user", "logsum@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-logsum")
	RecordLog(u.Id, LogTypeTopup, "topup for sum")

	stat := SumUsedQuota(LogTypeTopup, 0, 0, "", "", "", 0, "")
	_ = stat // just verify no panic
}

func TestLogRepo_GetUserLogsInternal(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "logint-user", "logint@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-logint")
	RecordLog(u.Id, LogTypeTopup, "internal log 1")
	RecordLog(u.Id, LogTypeTopup, "internal log 2")

	logs, total, err := GetUserLogsInternal(u.Id, 0, 10)
	if err != nil {
		t.Fatalf("GetUserLogsInternal: %v", err)
	}
	if total < 2 {
		t.Errorf("GetUserLogsInternal total = %d, want >= 2", total)
	}
	_ = logs
}

func TestLogRepo_GetUserLogsWithParams(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "logparams-user", "logparams@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-logparams")
	RecordLog(u.Id, LogTypeTopup, "params log")

	params := &LogQueryParams{
		UserID:  u.Id,
		LogType: LogTypeTopup,
		Offset:  0,
		Limit:   10,
	}
	logs, total, err := GetUserLogsWithParams(params)
	if err != nil {
		t.Fatalf("GetUserLogsWithParams: %v", err)
	}
	if total < 1 {
		t.Errorf("GetUserLogsWithParams total = %d, want >= 1", total)
	}
	_ = logs
}

// ─── InternalApiKey ───────────────────────────────────────────────────────────

func TestInternalApiKey_CreateAndValidate(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	key, apiKey, err := CreateInternalApiKey("test-key", []string{ScopeUserRead}, 1, 0, "test desc")
	if err != nil {
		t.Fatalf("CreateInternalApiKey: %v", err)
	}
	if key == "" || apiKey == nil || apiKey.Id == 0 {
		t.Fatal("CreateInternalApiKey returned empty key or nil record")
	}

	validated, err := ValidateInternalApiKey(key)
	if err != nil {
		t.Fatalf("ValidateInternalApiKey: %v", err)
	}
	if validated.Id != apiKey.Id {
		t.Errorf("ValidateInternalApiKey id = %d, want %d", validated.Id, apiKey.Id)
	}

	// Bad key
	_, err = ValidateInternalApiKey("bad-key")
	if err == nil {
		t.Error("expected error for bad key")
	}
}

func TestInternalApiKey_GetAll(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	CreateInternalApiKey("key-a", []string{ScopeUserRead}, 1, 0, "")
	CreateInternalApiKey("key-b", []string{ScopeUserWrite}, 1, 0, "")

	keys, err := GetAllInternalApiKeys()
	if err != nil {
		t.Fatalf("GetAllInternalApiKeys: %v", err)
	}
	if len(keys) < 2 {
		t.Errorf("GetAllInternalApiKeys count = %d, want >= 2", len(keys))
	}
}

func TestInternalApiKey_GetByID(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	_, apiKey, err := CreateInternalApiKey("byid-key", []string{ScopeAdmin}, 1, 0, "byid")
	if err != nil {
		t.Fatalf("CreateInternalApiKey: %v", err)
	}

	got, err := GetInternalApiKeyById(apiKey.Id)
	if err != nil {
		t.Fatalf("GetInternalApiKeyById: %v", err)
	}
	if got.Name != "byid-key" {
		t.Errorf("name = %q, want 'byid-key'", got.Name)
	}
}

func TestInternalApiKey_Delete(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	_, apiKey, err := CreateInternalApiKey("del-key", []string{ScopeUserRead}, 1, 0, "")
	if err != nil {
		t.Fatalf("CreateInternalApiKey: %v", err)
	}

	if err := DeleteInternalApiKey(apiKey.Id); err != nil {
		t.Fatalf("DeleteInternalApiKey: %v", err)
	}
	_, err = GetInternalApiKeyById(apiKey.Id)
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
}

func TestInternalApiKey_Toggle(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	_, apiKey, err := CreateInternalApiKey("toggle-key", []string{ScopeUserRead}, 1, 0, "")
	if err != nil {
		t.Fatalf("CreateInternalApiKey: %v", err)
	}
	if !apiKey.Enabled {
		t.Fatal("key should be enabled after creation")
	}

	if err := ToggleInternalApiKey(apiKey.Id); err != nil {
		t.Fatalf("ToggleInternalApiKey: %v", err)
	}
	got, _ := GetInternalApiKeyById(apiKey.Id)
	if got.Enabled {
		t.Error("key should be disabled after toggle")
	}

	// Toggle back
	if err := ToggleInternalApiKey(apiKey.Id); err != nil {
		t.Fatalf("ToggleInternalApiKey 2: %v", err)
	}
	got, _ = GetInternalApiKeyById(apiKey.Id)
	if !got.Enabled {
		t.Error("key should be re-enabled after 2nd toggle")
	}
}

func TestInternalApiKey_Update(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	_, apiKey, err := CreateInternalApiKey("upd-key", []string{ScopeUserRead}, 1, 0, "old desc")
	if err != nil {
		t.Fatalf("CreateInternalApiKey: %v", err)
	}

	if err := UpdateInternalApiKey(apiKey.Id, "upd-key-renamed", []string{ScopeAdmin}, 0, "new desc"); err != nil {
		t.Fatalf("UpdateInternalApiKey: %v", err)
	}
	got, _ := GetInternalApiKeyById(apiKey.Id)
	if got.Name != "upd-key-renamed" {
		t.Errorf("name = %q, want 'upd-key-renamed'", got.Name)
	}
	if got.Description != "new desc" {
		t.Errorf("description = %q, want 'new desc'", got.Description)
	}
}

func TestInternalApiKey_AllowedForTenant(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	// nil guard
	if InternalKeyAllowedForTenant(nil, "any-tenant") {
		t.Error("nil key should be denied")
	}

	// ScopeAll key bypasses whitelist
	_, allKey, err := CreateInternalApiKey("all-scope", []string{ScopeAll}, 1, 0, "")
	if err != nil {
		t.Fatalf("CreateInternalApiKey: %v", err)
	}
	if !InternalKeyAllowedForTenant(allKey, "any-tenant-xyz") {
		t.Error("ScopeAll key should be allowed for any tenant")
	}

	// Narrow-scope key without whitelist entry → deny
	_, narrowKey, err := CreateInternalApiKey("narrow-scope", []string{ScopeProvisioning}, 1, 0, "")
	if err != nil {
		t.Fatalf("CreateInternalApiKey: %v", err)
	}
	if InternalKeyAllowedForTenant(narrowKey, "some-tenant") {
		t.Error("narrow-scope key without whitelist entry should be denied")
	}
}

func TestInternalApiKey_GetAvailableScopes(t *testing.T) {
	scopes := GetAvailableScopes()
	if len(scopes) == 0 {
		t.Error("GetAvailableScopes returned empty")
	}
	for _, s := range scopes {
		if s["key"] == "" || s["name"] == "" {
			t.Errorf("scope entry missing key/name: %v", s)
		}
	}
}

// ─── Option ───────────────────────────────────────────────────────────────────

func TestOptionRepo_AllOption(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	opts, err := AllOption()
	if err != nil {
		t.Fatalf("AllOption: %v", err)
	}
	_ = opts // empty on fresh DB is fine
}

func TestOptionRepo_UpdateOption(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	if err := UpdateOption("TestKey", "TestValue"); err != nil {
		t.Fatalf("UpdateOption (create): %v", err)
	}

	// Update should overwrite
	if err := UpdateOption("TestKey", "NewValue"); err != nil {
		t.Fatalf("UpdateOption (update): %v", err)
	}

	opts, _ := AllOption()
	var found bool
	for _, o := range opts {
		if o.Key == "TestKey" && o.Value == "NewValue" {
			found = true
			break
		}
	}
	if !found {
		t.Error("UpdateOption: updated value not found in AllOption")
	}
}

// ─── Tenant — credit pool ─────────────────────────────────────────────────────

func TestTenantCreditPool_CreateAndGet(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	seedTenant(t, "pool-tenant", "pool-slug", "Pool Tenant")

	// Expect ErrPoolNotFound before creation
	_, err := GetTenantCreditPool("pool-tenant")
	if err == nil {
		t.Log("pool already exists (ok for idempotent tests)")
	}

	created, err := CreateTenantCreditPool("pool-tenant", 1, PoolMaxBalanceUnlimited, PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("CreateTenantCreditPool: %v", err)
	}
	if created.TenantID != "pool-tenant" {
		t.Errorf("pool TenantID = %q, want 'pool-tenant'", created.TenantID)
	}
	if created.CurrentBalance != 0 {
		t.Errorf("initial CurrentBalance = %d, want 0", created.CurrentBalance)
	}

	// GetTenantCreditPool should return the created pool
	got, err := GetTenantCreditPool("pool-tenant")
	if err != nil {
		t.Fatalf("GetTenantCreditPool: %v", err)
	}
	if got.TenantID != "pool-tenant" {
		t.Errorf("GetTenantCreditPool TenantID = %q", got.TenantID)
	}
}

func TestTenantCreditPool_TopupAndDebit(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	seedTenant(t, "topup-pool-tenant", "topup-pool", "Topup Pool Tenant")
	pool, err := CreateTenantCreditPool("topup-pool-tenant", 1, 20000, PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("CreateTenantCreditPool: %v", err)
	}

	// Topup: TopupPool(poolID, tenantID, amount, actorUserID, reason)
	_, err = TopupPool(pool.ID, "topup-pool-tenant", 10000, 1, "test topup")
	if err != nil {
		t.Fatalf("TopupPool: %v", err)
	}

	got, err := GetTenantCreditPool("topup-pool-tenant")
	if err != nil {
		t.Fatalf("GetTenantCreditPool: %v", err)
	}
	if got.CurrentBalance != 10000 {
		t.Errorf("balance after topup = %d, want 10000", got.CurrentBalance)
	}

	// Debit: DebitPool(poolID, tenantID, amount, tokenID, logID)
	if err := DebitPool(pool.ID, "topup-pool-tenant", 3000, 0, 0); err != nil {
		t.Fatalf("DebitPool: %v", err)
	}

	got, err = GetTenantCreditPool("topup-pool-tenant")
	if err != nil {
		t.Fatalf("GetTenantCreditPool after debit: %v", err)
	}
	if got.CurrentBalance != 7000 {
		t.Errorf("balance after debit = %d, want 7000", got.CurrentBalance)
	}
}

// ─── GetUserEmail / GetUserGroup ─────────────────────────────────────────────

func TestUserRepo_GetUserEmail(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "email-check-user", "emailcheck@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-ec")

	email, err := GetUserEmail(u.Id)
	if err != nil {
		t.Fatalf("GetUserEmail: %v", err)
	}
	if email != u.Email {
		t.Errorf("email = %q, want %q", email, u.Email)
	}
}

func TestUserRepo_GetUsernameById(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "unamebyid-user", "unamebyid@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-un")

	name, err := GetUsernameById(u.Id, true)
	if err != nil {
		t.Fatalf("GetUsernameById: %v", err)
	}
	if name != u.Username {
		t.Errorf("username = %q, want %q", name, u.Username)
	}
}

// ─── Redemption — extended ───────────────────────────────────────────────────

func TestRedemptionRepo_GetAllRedemptions(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "rdm-all-user", "rdmall@test.local", common.RoleAdminUser, common.UserStatusEnabled, "tenant-rdmall")
	for i := 0; i < 3; i++ {
		seedRedemptionRow(t, u.Id, "tenant-rdmall", fmt.Sprintf("rdm-all-%d", i), 1000)
	}

	rdms, total, err := GetAllRedemptions(0, 10)
	if err != nil {
		t.Fatalf("GetAllRedemptions: %v", err)
	}
	if total < 3 {
		t.Errorf("GetAllRedemptions total = %d, want >= 3", total)
	}
	_ = rdms
}

func TestRedemptionRepo_SearchRedemptionsByTenant(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "rdm-search-user", "rdmsearch@test.local", common.RoleAdminUser, common.UserStatusEnabled, "tenant-rdmsearch")
	seedRedemptionRow(t, u.Id, "tenant-rdmsearch", "findable-rdm", 500)
	seedRedemptionRow(t, u.Id, "tenant-rdmsearch", "other-rdm", 500)

	rdms, total, err := SearchRedemptionsByTenant("tenant-rdmsearch", "findable", 0, 10)
	if err != nil {
		t.Fatalf("SearchRedemptionsByTenant: %v", err)
	}
	if total != 1 {
		t.Errorf("SearchRedemptionsByTenant total = %d, want 1", total)
	}
	if !strings.Contains(rdms[0].Name, "findable") {
		t.Errorf("SearchRedemptionsByTenant name = %q, want to contain 'findable'", rdms[0].Name)
	}
}

func TestRedemptionRepo_GetAllRedemptionsByTenantPaged(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "rdm-paged-user", "rdmpaged@test.local", common.RoleAdminUser, common.UserStatusEnabled, "tenant-rdmpaged")
	for i := 0; i < 5; i++ {
		seedRedemptionRow(t, u.Id, "tenant-rdmpaged", fmt.Sprintf("rdm-paged-%d", i), 100)
	}

	rdms, total, err := GetRedemptionsByTenant("tenant-rdmpaged", 0, 3)
	if err != nil {
		t.Fatalf("GetRedemptionsByTenant: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(rdms) != 3 {
		t.Errorf("page = %d, want 3", len(rdms))
	}
}

// ─── Channel — struct-method tests (pure logic, no DB required) ───────────────

func TestChannelRepo_GetKeys_Variants(t *testing.T) {
	ch := &Channel{}
	if got := ch.GetKeys(); len(got) != 0 {
		t.Errorf("empty key: GetKeys = %v, want []", got)
	}

	ch = &Channel{Key: "sk-abc"}
	keys := ch.GetKeys()
	if len(keys) != 1 || keys[0] != "sk-abc" {
		t.Errorf("single key: GetKeys = %v", keys)
	}

	ch = &Channel{Key: "key1\nkey2\nkey3"}
	keys = ch.GetKeys()
	if len(keys) != 3 {
		t.Errorf("multi-key: GetKeys len = %d, want 3", len(keys))
	}
}

func TestChannelRepo_GetModels(t *testing.T) {
	ch := &Channel{Models: "gpt-4,gpt-3.5-turbo,claude-3"}
	models := ch.GetModels()
	if len(models) != 3 {
		t.Errorf("GetModels len = %d, want 3", len(models))
	}

	empty := &Channel{Models: ""}
	if got := empty.GetModels(); len(got) != 0 {
		t.Errorf("empty GetModels = %v", got)
	}
}

func TestChannelRepo_GetGroups(t *testing.T) {
	ch := &Channel{Group: "default,premium"}
	groups := ch.GetGroups()
	if len(groups) != 2 {
		t.Errorf("GetGroups len = %d, want 2", len(groups))
	}
	if groups[0] != "default" || groups[1] != "premium" {
		t.Errorf("GetGroups = %v", groups)
	}

	empty := &Channel{Group: ""}
	if got := empty.GetGroups(); len(got) != 0 {
		t.Errorf("empty GetGroups = %v", got)
	}
}

func TestChannelRepo_GetTag(t *testing.T) {
	ch := &Channel{}
	if ch.GetTag() != "" {
		t.Error("nil tag should return empty string")
	}

	ch.SetTag("production")
	if ch.GetTag() != "production" {
		t.Errorf("GetTag = %q, want 'production'", ch.GetTag())
	}
}

func TestChannelRepo_GetAutoBan(t *testing.T) {
	ch := &Channel{}
	if ch.GetAutoBan() {
		t.Error("nil AutoBan should return false")
	}

	on := 1
	ch.AutoBan = &on
	if !ch.GetAutoBan() {
		t.Error("AutoBan=1 should return true")
	}

	off := 0
	ch.AutoBan = &off
	if ch.GetAutoBan() {
		t.Error("AutoBan=0 should return false")
	}
}

func TestChannelRepo_GetPriorityAndWeight(t *testing.T) {
	ch := &Channel{}
	if ch.GetPriority() != 0 {
		t.Errorf("nil priority = %d, want 0", ch.GetPriority())
	}
	if ch.GetWeight() != 0 {
		t.Errorf("nil weight = %d, want 0", ch.GetWeight())
	}

	p := int64(100)
	w := uint(50)
	ch.Priority = &p
	ch.Weight = &w
	if ch.GetPriority() != 100 {
		t.Errorf("priority = %d, want 100", ch.GetPriority())
	}
	if ch.GetWeight() != 50 {
		t.Errorf("weight = %d, want 50", ch.GetWeight())
	}
}

func TestChannelRepo_GetBaseURL(t *testing.T) {
	ch := &Channel{}
	_ = ch.GetBaseURL()

	url := "https://example.com"
	ch.BaseURL = &url
	if ch.GetBaseURL() != "https://example.com" {
		t.Errorf("GetBaseURL = %q, want 'https://example.com'", ch.GetBaseURL())
	}
}

func TestChannelRepo_GetModelMapping(t *testing.T) {
	ch := &Channel{}
	if ch.GetModelMapping() != "" {
		t.Error("nil model mapping should return empty string")
	}

	mm := `{"gpt-4": "gpt-4-turbo"}`
	ch.ModelMapping = &mm
	if ch.GetModelMapping() != mm {
		t.Errorf("GetModelMapping = %q", ch.GetModelMapping())
	}
}

func TestChannelRepo_GetStatusCodeMapping(t *testing.T) {
	ch := &Channel{}
	if ch.GetStatusCodeMapping() != "" {
		t.Error("nil status code mapping should return empty string")
	}

	scm := `{"429": "rate_limit"}`
	ch.StatusCodeMapping = &scm
	if ch.GetStatusCodeMapping() != scm {
		t.Errorf("GetStatusCodeMapping = %q", ch.GetStatusCodeMapping())
	}
}

func TestChannelRepo_GetOtherInfoAndSet(t *testing.T) {
	ch := &Channel{}
	info := ch.GetOtherInfo()
	if info == nil || len(info) != 0 {
		t.Errorf("GetOtherInfo on empty = %v", info)
	}

	ch.SetOtherInfo(map[string]interface{}{"status_reason": "test"})
	got := ch.GetOtherInfo()
	if got["status_reason"] != "test" {
		t.Errorf("GetOtherInfo status_reason = %v", got["status_reason"])
	}
}

func TestChannelRepo_ManagedModelsBySync(t *testing.T) {
	ch := &Channel{}
	if got := ch.GetManagedModelsBySync(); len(got) != 0 {
		t.Errorf("empty managed models = %v", got)
	}

	if err := ch.SetManagedModelsBySync([]string{"model-a", "model-b"}); err != nil {
		t.Fatalf("SetManagedModelsBySync: %v", err)
	}
	got := ch.GetManagedModelsBySync()
	if len(got) != 2 || got[0] != "model-a" {
		t.Errorf("GetManagedModelsBySync = %v", got)
	}

	if err := ch.SetManagedModelsBySync(nil); err != nil {
		t.Fatalf("SetManagedModelsBySync nil: %v", err)
	}
	if got = ch.GetManagedModelsBySync(); len(got) != 0 {
		t.Errorf("nil input result = %v", got)
	}
}

func TestChannelRepo_BatchInsertAndDelete(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	chs := []Channel{
		{TenantId: "batch-tenant", Name: "batch-ch-1", Type: 1, Key: "k1", Status: 1, Models: "gpt-4", Group: "default"},
		{TenantId: "batch-tenant", Name: "batch-ch-2", Type: 1, Key: "k2", Status: 1, Models: "gpt-3.5", Group: "default"},
		{TenantId: "batch-tenant", Name: "batch-ch-3", Type: 1, Key: "k3", Status: 1, Models: "claude-3", Group: "default"},
	}
	if err := BatchInsertChannels(chs); err != nil {
		t.Fatalf("BatchInsertChannels: %v", err)
	}

	inserted, err := GetChannelsByTenant("batch-tenant", 0, 10, true)
	if err != nil {
		t.Fatalf("GetChannelsByTenant: %v", err)
	}
	if len(inserted) != 3 {
		t.Errorf("after BatchInsert: count = %d, want 3", len(inserted))
	}

	ids := make([]int, len(inserted))
	for i, c := range inserted {
		ids[i] = c.Id
	}

	if err := BatchDeleteChannels(ids); err != nil {
		t.Fatalf("BatchDeleteChannels: %v", err)
	}

	after, err := GetChannelsByTenant("batch-tenant", 0, 10, true)
	if err != nil {
		t.Fatalf("GetChannelsByTenant after delete: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("after BatchDelete: count = %d, want 0", len(after))
	}
}

func TestChannelRepo_UpdateChannelModels(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := seedChannel(t, "models-ch", "tenant-models")

	if err := UpdateChannelModels(ch.Id, "claude-3,gemini-pro"); err != nil {
		t.Fatalf("UpdateChannelModels: %v", err)
	}
	got, err := GetChannelById(ch.Id, false)
	if err != nil {
		t.Fatalf("GetChannelById: %v", err)
	}
	if got.Models != "claude-3,gemini-pro" {
		t.Errorf("models = %q, want 'claude-3,gemini-pro'", got.Models)
	}
}

func TestChannelRepo_SaveWithoutKey(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := seedChannel(t, "nokey-ch", "tenant-nokey")
	origKey := ch.Key

	ch.Name = "nokey-updated"
	if err := ch.SaveWithoutKey(); err != nil {
		t.Fatalf("SaveWithoutKey: %v", err)
	}

	got, err := GetChannelById(ch.Id, true)
	if err != nil {
		t.Fatalf("GetChannelById: %v", err)
	}
	if got.Name != "nokey-updated" {
		t.Errorf("name = %q, want 'nokey-updated'", got.Name)
	}
	if got.Key != origKey {
		t.Errorf("key changed: got %q, want %q", got.Key, origKey)
	}

	zero := &Channel{}
	if err := zero.SaveWithoutKey(); err == nil {
		t.Error("SaveWithoutKey with id=0 should return error")
	}
}

func TestChannelRepo_GetChannelsByIds(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch1 := seedChannel(t, "byids-ch1", "tenant-byids")
	ch2 := seedChannel(t, "byids-ch2", "tenant-byids")
	seedChannel(t, "byids-ch3", "tenant-byids")

	got, err := GetChannelsByIds([]int{ch1.Id, ch2.Id})
	if err != nil {
		t.Fatalf("GetChannelsByIds: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("GetChannelsByIds count = %d, want 2", len(got))
	}

	empty, err := GetChannelsByIds([]int{})
	if err != nil {
		t.Fatalf("GetChannelsByIds empty: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("empty ids should return 0, got %d", len(empty))
	}
}

func TestChannelRepo_GetChannelPollingLock(t *testing.T) {
	lock1 := GetChannelPollingLock(9999)
	lock2 := GetChannelPollingLock(9999)
	if lock1 != lock2 {
		t.Error("GetChannelPollingLock should return same mutex for same channel id")
	}
}

// ─── User — remaining methods ─────────────────────────────────────────────────

func TestUserRepo_FillUserById(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "fill-by-id", "fillbyid@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-fill")

	filled := &User{Id: u.Id}
	if err := filled.FillUserById(); err != nil {
		t.Fatalf("FillUserById: %v", err)
	}
	if filled.Username != u.Username {
		t.Errorf("username = %q, want %q", filled.Username, u.Username)
	}

	zero := &User{Id: 0}
	if err := zero.FillUserById(); err == nil {
		t.Error("FillUserById with id=0 should return error")
	}
}

func TestUserRepo_GetAccessToken(t *testing.T) {
	u := &User{}
	if u.GetAccessToken() != "" {
		t.Error("nil AccessToken should return empty string")
	}

	u.SetAccessToken("my-access-token")
	if u.GetAccessToken() != "my-access-token" {
		t.Errorf("GetAccessToken = %q, want 'my-access-token'", u.GetAccessToken())
	}
}

func TestUserRepo_ToBaseUser(t *testing.T) {
	u := &User{
		Username:    "base-user",
		DisplayName: "Base User",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Email:       "base@test.local",
	}
	base := u.ToBaseUser()
	if base == nil {
		t.Fatal("ToBaseUser returned nil")
	}
	if base.Username != u.Username {
		t.Errorf("base.Username = %q, want %q", base.Username, u.Username)
	}
}

func TestUserRepo_GetUserByLurusAccountID(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	_, err := GetUserByLurusAccountID(0)
	if err == nil {
		t.Error("expected error for account_id=0, got nil")
	}

	_, err = GetUserByLurusAccountID(999999)
	if err == nil {
		t.Error("expected error for non-existent account_id, got nil")
	}
}

func TestUserRepo_GetUserSetting(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "setting-user", "setting@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-setting")

	setting, err := GetUserSetting(u.Id, true)
	if err != nil {
		t.Fatalf("GetUserSetting: %v", err)
	}
	_ = setting
}

func TestUserRepo_GetUserGroup(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "group-user", "group@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-group")

	group, err := GetUserGroup(u.Id, true)
	if err != nil {
		t.Fatalf("GetUserGroup: %v", err)
	}
	_ = group
}

func TestUserRepo_GetUserUsedQuota(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "used-quota-user", "usedq@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-usedq")

	quota, err := GetUserUsedQuota(u.Id)
	if err != nil {
		t.Fatalf("GetUserUsedQuota: %v", err)
	}
	if quota < 0 {
		t.Errorf("GetUserUsedQuota = %d, want >= 0", quota)
	}
}

func TestUserRepo_HardDeleteMethod(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "hard-del-method-user", "hdm@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-hdm")

	if err := u.HardDelete(); err != nil {
		t.Fatalf("HardDelete: %v", err)
	}

	var found User
	err := DB.Unscoped().First(&found, u.Id).Error
	if err == nil {
		t.Error("expected record-not-found after HardDelete, got nil error")
	}

	zero := &User{}
	if err := zero.HardDelete(); err == nil {
		t.Error("HardDelete with id=0 should return error")
	}
}

func TestTokenRepo_UpdateToken(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "tok-update-user", "tokupdate@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-tokupdate")
	tok := &Token{UserId: u.Id, TenantId: "tenant-tokupdate", Name: "update-tok", Key: common.GetRandomString(48), Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1}
	if err := tok.Insert(); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	tok.Name = "renamed-tok"
	if err := tok.Update(); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := GetTokenById(tok.Id)
	if got.Name != "renamed-tok" {
		t.Errorf("name = %q, want 'renamed-tok'", got.Name)
	}
}

func TestTokenRepo_CleanToken(t *testing.T) {
	tok := &Token{Key: "sk-secret-key"}
	tok.Clean()
	if tok.Key != "" {
		t.Errorf("Clean() should erase key, got %q", tok.Key)
	}
}

func TestTokenRepo_ModelLimits(t *testing.T) {
	tok := &Token{ModelLimits: "gpt-4,claude-3", ModelLimitsEnabled: true}

	if !tok.IsModelLimitsEnabled() {
		t.Error("IsModelLimitsEnabled should be true when ModelLimitsEnabled=true")
	}
	limits := tok.GetModelLimits()
	if len(limits) != 2 {
		t.Errorf("GetModelLimits = %v, want 2 elements", limits)
	}
	m := tok.GetModelLimitsMap()
	if !m["gpt-4"] {
		t.Error("GetModelLimitsMap missing gpt-4")
	}

	empty := &Token{}
	if empty.IsModelLimitsEnabled() {
		t.Error("empty token should not have model limits enabled")
	}
}

func TestTokenRepo_BatchDeleteTokens(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "tok-batch-del-user", "tbdel@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-tbdel")

	var ids []int
	for i := 0; i < 3; i++ {
		tok := &Token{UserId: u.Id, TenantId: "tenant-tbdel", Name: fmt.Sprintf("batch-del-%d", i), Key: common.GetRandomString(48), Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1}
		if err := tok.Insert(); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		ids = append(ids, tok.Id)
	}

	n, err := BatchDeleteTokens(ids, u.Id)
	if err != nil {
		t.Fatalf("BatchDeleteTokens: %v", err)
	}
	if n != 3 {
		t.Errorf("BatchDeleteTokens deleted = %d, want 3", n)
	}
}

// ─── TenantConfig ─────────────────────────────────────────────────────────────

func TestTenantConfigSQLite_SetAndGet(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	seedTenant(t, "cfg-tenant", "cfg-slug", "Config Tenant")

	if err := SetTenantConfig("cfg-tenant", "quota_limit", "5000", "integer", "quota limit", false); err != nil {
		t.Fatalf("SetTenantConfig: %v", err)
	}

	cfg, err := GetTenantConfig("cfg-tenant", "quota_limit")
	if err != nil {
		t.Fatalf("GetTenantConfig: %v", err)
	}
	if cfg.ConfigValue != "5000" {
		t.Errorf("value = %q, want '5000'", cfg.ConfigValue)
	}

	val := GetTenantConfigValue("cfg-tenant", "quota_limit", "0")
	if val != "5000" {
		t.Errorf("GetTenantConfigValue = %q, want '5000'", val)
	}

	def := GetTenantConfigValue("cfg-tenant", "nonexistent", "fallback")
	if def != "fallback" {
		t.Errorf("GetTenantConfigValue default = %q, want 'fallback'", def)
	}
}

func TestTenantConfig_SetAndGetInt(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	seedTenant(t, "cfgint-tenant", "cfgint-slug", "Config Int Tenant")

	if err := SetTenantConfigInt("cfgint-tenant", "max_users", 100, "max users"); err != nil {
		t.Fatalf("SetTenantConfigInt: %v", err)
	}

	val := GetTenantConfigInt("cfgint-tenant", "max_users", 0)
	if val != 100 {
		t.Errorf("GetTenantConfigInt = %d, want 100", val)
	}
}

func TestTenantConfig_SetAndGetBool(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	seedTenant(t, "cfgbool-tenant", "cfgbool-slug", "Config Bool Tenant")

	if err := SetTenantConfigBool("cfgbool-tenant", "feature_x", true, "feature x"); err != nil {
		t.Fatalf("SetTenantConfigBool: %v", err)
	}

	val := GetTenantConfigBool("cfgbool-tenant", "feature_x", false)
	if !val {
		t.Error("GetTenantConfigBool should return true")
	}
}

func TestTenantConfig_ListAndDelete(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	seedTenant(t, "cfglist-tenant", "cfglist-slug", "Config List Tenant")

	for _, key := range []string{"key-a", "key-b", "key-c"} {
		if err := SetTenantConfig("cfglist-tenant", key, "v", "string", "", false); err != nil {
			t.Fatalf("SetTenantConfig %q: %v", key, err)
		}
	}

	cfgs, err := ListTenantConfigs("cfglist-tenant", false)
	if err != nil {
		t.Fatalf("ListTenantConfigs: %v", err)
	}
	if len(cfgs) != 3 {
		t.Errorf("ListTenantConfigs count = %d, want 3", len(cfgs))
	}

	if err := DeleteTenantConfig("cfglist-tenant", "key-b"); err != nil {
		t.Fatalf("DeleteTenantConfig: %v", err)
	}

	cfgs, err = ListTenantConfigs("cfglist-tenant", false)
	if err != nil {
		t.Fatalf("ListTenantConfigs after delete: %v", err)
	}
	if len(cfgs) != 2 {
		t.Errorf("ListTenantConfigs after delete = %d, want 2", len(cfgs))
	}
}

// ─── UserMapping ──────────────────────────────────────────────────────────────

func TestUserMappingRepo_CreateAndGet(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "mapping-user", "mapping@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-mapping")
	seedTenant(t, "tenant-mapping", "mapping-slug", "Mapping Tenant")

	mapping, err := CreateUserMapping(u.Id, "zitadel-sub-001", "tenant-mapping", "mapping@test.local", "Mapping User", "mapping-user")
	if err != nil {
		t.Fatalf("CreateUserMapping: %v", err)
	}
	if mapping.LurusUserID != u.Id {
		t.Errorf("LurusUserID = %d, want %d", mapping.LurusUserID, u.Id)
	}

	got, err := GetUserMappingByIDPSubject("zitadel-sub-001", "tenant-mapping")
	if err != nil {
		t.Fatalf("GetUserMappingByIDPSubject: %v", err)
	}
	if got.Id != mapping.Id {
		t.Errorf("mapping id = %d, want %d", got.Id, mapping.Id)
	}

	got2, err := GetUserMappingByLurusUserID(u.Id, "tenant-mapping")
	if err != nil {
		t.Fatalf("GetUserMappingByLurusUserID: %v", err)
	}
	if got2.Id != mapping.Id {
		t.Errorf("mapping by lurus user id = %d, want %d", got2.Id, mapping.Id)
	}
}

func TestUserMappingRepo_ListByTenant(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	seedTenant(t, "list-mapping-tenant", "list-mapping-slug", "List Mapping Tenant")

	for i := 0; i < 3; i++ {
		u := seedUser(t, fmt.Sprintf("lm-user-%d", i), fmt.Sprintf("lm%d@test.local", i), common.RoleCommonUser, common.UserStatusEnabled, "list-mapping-tenant")
		if _, err := CreateUserMapping(u.Id, fmt.Sprintf("zitadel-lm-%d", i), "list-mapping-tenant", fmt.Sprintf("lm%d@test.local", i), fmt.Sprintf("LM User %d", i), fmt.Sprintf("lm-user-%d", i)); err != nil {
			t.Fatalf("CreateUserMapping %d: %v", i, err)
		}
	}

	mappings, total, err := ListUserMappingsByTenant("list-mapping-tenant", 0, 10)
	if err != nil {
		t.Fatalf("ListUserMappingsByTenant: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(mappings) != 3 {
		t.Errorf("mappings len = %d, want 3", len(mappings))
	}
}

func TestUserMappingRepo_DeactivateAndDelete(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "deact-mapping-user", "deact@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-deact")
	seedTenant(t, "tenant-deact", "deact-slug", "Deact Tenant")

	mapping, err := CreateUserMapping(u.Id, "zitadel-deact-001", "tenant-deact", "deact@test.local", "Deact User", "deact-user")
	if err != nil {
		t.Fatalf("CreateUserMapping: %v", err)
	}

	if err := DeactivateUserMapping(mapping.Id); err != nil {
		t.Fatalf("DeactivateUserMapping: %v", err)
	}

	if err := DeleteUserMapping(mapping.Id); err != nil {
		t.Fatalf("DeleteUserMapping: %v", err)
	}
}

// ─── Channel — DB operations (round 3) ───────────────────────────────────────

func TestChannelRepo_UpdateAndUpdateBalance(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := seedChannel(t, "update-ch", "tenant-update")

	ch.Name = "updated-ch"
	ch.Models = "new-model"
	if err := ch.Update(); err != nil {
		t.Fatalf("Channel.Update: %v", err)
	}

	got, err := GetChannelById(ch.Id, false)
	if err != nil {
		t.Fatalf("GetChannelById: %v", err)
	}
	if got.Name != "updated-ch" {
		t.Errorf("name = %q, want 'updated-ch'", got.Name)
	}

	ch.UpdateBalance(99.5)
	got2, _ := GetChannelById(ch.Id, false)
	if got2.Balance != 99.5 {
		t.Errorf("balance = %f, want 99.5", got2.Balance)
	}
}

func TestChannelRepo_UpdateChannelUsedQuota(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := seedChannel(t, "used-quota-ch", "tenant-uq")

	UpdateChannelUsedQuota(ch.Id, 500)

	got, err := GetChannelById(ch.Id, false)
	if err != nil {
		t.Fatalf("GetChannelById: %v", err)
	}
	if got.UsedQuota != 500 {
		t.Errorf("UsedQuota = %d, want 500", got.UsedQuota)
	}
}

func TestChannelRepo_EnableDisableByTag(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	tag := "enable-disable-tag"
	ch1 := seedChannel(t, "en-ch1", "tenant-en")
	ch2 := seedChannel(t, "en-ch2", "tenant-en")
	ch1.SetTag(tag)
	ch2.SetTag(tag)
	if err := ch1.Save(); err != nil {
		t.Fatalf("Save ch1: %v", err)
	}
	if err := ch2.Save(); err != nil {
		t.Fatalf("Save ch2: %v", err)
	}

	if err := DisableChannelByTag(tag); err != nil {
		t.Fatalf("DisableChannelByTag: %v", err)
	}
	got, _ := GetChannelById(ch1.Id, false)
	if got.Status != common.ChannelStatusManuallyDisabled {
		t.Errorf("after DisableByTag status = %d, want manually disabled (%d)", got.Status, common.ChannelStatusManuallyDisabled)
	}

	if err := EnableChannelByTag(tag); err != nil {
		t.Fatalf("EnableChannelByTag: %v", err)
	}
	got, _ = GetChannelById(ch1.Id, false)
	if got.Status != common.ChannelStatusEnabled {
		t.Errorf("after EnableByTag status = %d, want enabled (%d)", got.Status, common.ChannelStatusEnabled)
	}
}

func TestChannelRepo_DeleteChannelByStatus(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := seedChannel(t, "to-disable-ch", "tenant-tobedisabled")
	if err := DB.Model(ch).Update("status", common.ChannelStatusManuallyDisabled).Error; err != nil {
		t.Fatalf("set status: %v", err)
	}

	n, err := DeleteChannelByStatus(int64(common.ChannelStatusManuallyDisabled))
	if err != nil {
		t.Fatalf("DeleteChannelByStatus: %v", err)
	}
	if n < 1 {
		t.Errorf("DeleteChannelByStatus deleted = %d, want >= 1", n)
	}
}

func TestChannelRepo_DeleteDisabledChannel(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := seedChannel(t, "auto-disabled-ch", "tenant-autodisabled")
	if err := DB.Model(ch).Update("status", common.ChannelStatusAutoDisabled).Error; err != nil {
		t.Fatalf("set status: %v", err)
	}

	n, err := DeleteDisabledChannel()
	if err != nil {
		t.Fatalf("DeleteDisabledChannel: %v", err)
	}
	if n < 1 {
		t.Errorf("DeleteDisabledChannel deleted = %d, want >= 1", n)
	}
}

func TestChannelRepo_GetPaginatedTags(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	for i, tagName := range []string{"tag-alpha", "tag-beta", "tag-gamma"} {
		ch := seedChannel(t, fmt.Sprintf("tagged-ch-%d", i), "tenant-tags")
		ch.SetTag(tagName)
		if err := ch.Save(); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	tags, err := GetPaginatedTags(0, 10)
	if err != nil {
		t.Fatalf("GetPaginatedTags: %v", err)
	}
	if len(tags) < 3 {
		t.Errorf("GetPaginatedTags count = %d, want >= 3", len(tags))
	}
}

func TestChannelRepo_SearchTags(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	for _, tagName := range []string{"searchable-tag", "other-tag"} {
		ch := seedChannel(t, "search-tag-ch-"+tagName, "tenant-searchtag")
		ch.SetTag(tagName)
		if err := ch.Save(); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	tags, err := SearchTags("searchable", "", "", false)
	if err != nil {
		t.Fatalf("SearchTags: %v", err)
	}
	_ = tags // just verify no panic/error
}

func TestChannelRepo_ValidateSettingsGetSet(t *testing.T) {
	ch := &Channel{}

	// Nil setting is valid
	if err := ch.ValidateSettings(); err != nil {
		t.Errorf("ValidateSettings nil: %v", err)
	}

	// Get empty setting
	setting := ch.GetSetting()
	_ = setting

	// Set and get back
	ch.SetSetting(setting)
	if ch.Setting == nil {
		t.Error("Setting should not be nil after SetSetting")
	}

	got := ch.GetSetting()
	_ = got
}

func TestChannelRepo_GetSetOtherSettings(t *testing.T) {
	ch := &Channel{}

	s := ch.GetOtherSettings()
	_ = s

	ch.SetOtherSettings(s)
	if ch.OtherSettings == "" {
		t.Error("OtherSettings should not be empty after SetOtherSettings")
	}
}

func TestChannelRepo_GetParamAndHeaderOverride(t *testing.T) {
	ch := &Channel{}
	params := ch.GetParamOverride()
	if params == nil {
		t.Error("GetParamOverride should return non-nil map")
	}

	headers := ch.GetHeaderOverride()
	if headers == nil {
		t.Error("GetHeaderOverride should return non-nil map")
	}

	// With values
	paramJSON := `{"temperature": 0.7}`
	ch.ParamOverride = &paramJSON
	params = ch.GetParamOverride()
	if _, ok := params["temperature"]; !ok {
		t.Error("GetParamOverride should contain 'temperature'")
	}

	headerJSON := `{"X-Custom": "value"}`
	ch.HeaderOverride = &headerJSON
	headers = ch.GetHeaderOverride()
	if _, ok := headers["X-Custom"]; !ok {
		t.Error("GetHeaderOverride should contain 'X-Custom'")
	}
}

// TestChannelRepo_BatchSetChannelTag is skipped: BatchSetChannelTag opens a tx
// then calls GetChannelsByIds on the global DB, which deadlocks on SQLite's single-writer
// mode. This function is covered by handler integration tests.
func TestChannelRepo_BatchSetChannelTag(t *testing.T) {
	t.Skip("BatchSetChannelTag deadlocks on SQLite single-connection: covered by handler tests")
}

func TestChannelRepo_CountAllTags(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	for _, tagName := range []string{"cnt-tag-a", "cnt-tag-b"} {
		ch := seedChannel(t, "cnt-tag-ch-"+tagName, "tenant-cnttag")
		ch.SetTag(tagName)
		if err := ch.Save(); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	count, err := CountAllTags()
	if err != nil {
		t.Fatalf("CountAllTags: %v", err)
	}
	if count < 2 {
		t.Errorf("CountAllTags = %d, want >= 2", count)
	}
}

func TestChannelRepo_GetChannelsByType(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	for i := 0; i < 3; i++ {
		ch := &Channel{
			TenantId: "tenant-type", Name: fmt.Sprintf("type-ch-%d", i),
			Type: 5, Key: common.GetRandomString(32), Status: 1,
			Models: "gpt-4", Group: "default",
		}
		if err := ch.Insert(); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	chs, err := GetChannelsByType(0, 10, true, 5)
	if err != nil {
		t.Fatalf("GetChannelsByType: %v", err)
	}
	if len(chs) != 3 {
		t.Errorf("GetChannelsByType count = %d, want 3", len(chs))
	}

	count, err := CountChannelsByType(5)
	if err != nil {
		t.Fatalf("CountChannelsByType: %v", err)
	}
	if count != 3 {
		t.Errorf("CountChannelsByType = %d, want 3", count)
	}
}

func TestChannelRepo_SearchChannels(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := seedChannel(t, "searchable-channel-name", "tenant-srch")

	chs, err := SearchChannels("searchable-channel", "", "", true)
	if err != nil {
		t.Fatalf("SearchChannels: %v", err)
	}
	found := false
	for _, c := range chs {
		if c.Id == ch.Id {
			found = true
			break
		}
	}
	if !found {
		t.Error("SearchChannels did not find the seeded channel")
	}
}

func TestChannelRepo_CleanupChannelPollingLocks(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	// seed a channel so DB has known IDs
	seedChannel(t, "cleanup-lock-ch", "tenant-clup")

	// GetChannelPollingLock for a non-existent ID to populate the sync.Map
	GetChannelPollingLock(99888)

	// Cleanup should not panic
	CleanupChannelPollingLocks()
}

// ─── Tenant — additional ──────────────────────────────────────────────────────

func TestTenantRepo_SuspendTenant(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ten := seedTenant(t, "suspend-tenant", "suspend-slug", "Suspend Tenant")

	if err := SuspendTenant(ten.Id); err != nil {
		t.Fatalf("SuspendTenant: %v", err)
	}

	got, err := GetTenantByID(ten.Id)
	if err != nil {
		t.Fatalf("GetTenantByID: %v", err)
	}
	if got.Status != TenantStatusSuspended {
		t.Errorf("status = %d, want suspended", got.Status)
	}
}

func TestTenantRepo_GetTenantUserCount(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	seedTenant(t, "usercount-tenant", "usercount-slug", "User Count Tenant")

	// GetTenantUserCount counts UserIdentityMapping rows, not User rows
	for i := 0; i < 3; i++ {
		u := seedUser(t, fmt.Sprintf("uc-user-%d", i), fmt.Sprintf("uc%d@test.local", i), common.RoleCommonUser, common.UserStatusEnabled, "usercount-tenant")
		if _, err := CreateUserMapping(u.Id, fmt.Sprintf("zitadel-uc-%d", i), "usercount-tenant", fmt.Sprintf("uc%d@test.local", i), fmt.Sprintf("UC User %d", i), fmt.Sprintf("uc-user-%d", i)); err != nil {
			t.Fatalf("CreateUserMapping %d: %v", i, err)
		}
	}

	count, err := GetTenantUserCount("usercount-tenant")
	if err != nil {
		t.Fatalf("GetTenantUserCount: %v", err)
	}
	if count != 3 {
		t.Errorf("GetTenantUserCount = %d, want 3", count)
	}
}

func TestTenantRepo_GetTenantStats(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	seedTenant(t, "stats-tenant", "stats-slug", "Stats Tenant")

	stats, err := GetTenantStats("stats-tenant")
	if err != nil {
		t.Fatalf("GetTenantStats: %v", err)
	}
	_ = stats
}

func TestTenantRepo_GetTenantQuotaStats(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	seedTenant(t, "qstats-tenant", "qstats-slug", "Quota Stats Tenant")
	u := seedUser(t, "qstats-user", "qstats@test.local", common.RoleCommonUser, common.UserStatusEnabled, "qstats-tenant")
	if err := IncreaseUserQuota(u.Id, 10000, true); err != nil {
		t.Fatalf("IncreaseUserQuota: %v", err)
	}

	used, remaining, err := GetTenantQuotaStats("qstats-tenant")
	if err != nil {
		t.Fatalf("GetTenantQuotaStats: %v", err)
	}
	_ = used
	_ = remaining
}

func TestTenantRepo_GetTenantLogCount(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	seedTenant(t, "logcount-tenant", "logcount-slug", "Log Count Tenant")
	u := seedUser(t, "logcount-user", "logcount@test.local", common.RoleCommonUser, common.UserStatusEnabled, "logcount-tenant")
	RecordLogWithTenant(u.Id, "logcount-tenant", LogTypeTopup, "topup log")

	count, err := GetTenantLogCount("logcount-tenant")
	if err != nil {
		t.Fatalf("GetTenantLogCount: %v", err)
	}
	if count < 1 {
		t.Errorf("GetTenantLogCount = %d, want >= 1", count)
	}
}

func TestTenantRepo_GetTenantLastActivityTime(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	seedTenant(t, "lastact-tenant", "lastact-slug", "Last Activity Tenant")
	u := seedUser(t, "lastact-user", "lastact@test.local", common.RoleCommonUser, common.UserStatusEnabled, "lastact-tenant")
	RecordLogWithTenant(u.Id, "lastact-tenant", LogTypeTopup, "activity")

	ts, err := GetTenantLastActivityTime("lastact-tenant")
	if err != nil {
		t.Fatalf("GetTenantLastActivityTime: %v", err)
	}
	_ = ts
}

func TestTenantRepo_CreateFromZitadel(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ten, err := CreateTenantFromIDP("org-zitadel-001", "zitadel-domain.com", "Zitadel Org 001")
	if err != nil {
		t.Fatalf("CreateTenantFromIDP: %v", err)
	}
	if ten.IDPOrgID != "org-zitadel-001" {
		t.Errorf("ZitadelOrgID = %q", ten.IDPOrgID)
	}

	// Verify we can look it up
	got, err := GetTenantByIDPOrgID("org-zitadel-001")
	if err != nil {
		t.Fatalf("GetTenantByIDPOrgID: %v", err)
	}
	if got.Id != ten.Id {
		t.Errorf("tenant id mismatch: got %q, want %q", got.Id, ten.Id)
	}
}

// ─── Option — AllOption + UpdateOption (idempotency) ─────────────────────────

func TestOptionRepo_InitOptionMap(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	// Just verify no panic
	InitOptionMap()

	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	if common.OptionMap == nil {
		t.Error("InitOptionMap should initialize OptionMap")
	}
	if _, ok := common.OptionMap["SystemName"]; !ok {
		t.Error("OptionMap should contain SystemName after InitOptionMap")
	}
}

// ─── Ability ──────────────────────────────────────────────────────────────────

func TestAbilityRepo_GetEnabledModels(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := seedChannel(t, "ability-ch", "tenant-ability")
	_ = ch

	models := GetEnabledModels()
	_ = models // may be empty but must not panic
}

func TestAbilityRepo_GetGroupEnabledModels(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := seedChannel(t, "group-ability-ch", "tenant-gability")
	_ = ch

	models := GetGroupEnabledModels("default")
	_ = models
}

func TestAbilityRepo_GetAllEnableAbilities(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	abilities := GetAllEnableAbilities()
	_ = abilities
}

// ─── Log — extended ──────────────────────────────────────────────────────────

func TestLogRepo_SearchAllLogs(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "searchlog-user", "searchlog@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-searchlog")
	RecordLog(u.Id, LogTypeTopup, "unique-searchable-content-xyz")

	logs, err := SearchAllLogs("unique-searchable")
	if err != nil {
		t.Fatalf("SearchAllLogs: %v", err)
	}
	_ = logs
}

func TestLogRepo_SearchUserLogs(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "searchuserlog-user", "sulog@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-sulog")
	RecordLog(u.Id, LogTypeTopup, "user-specific-search-abc")

	logs, err := SearchUserLogs(u.Id, "user-specific")
	if err != nil {
		t.Fatalf("SearchUserLogs: %v", err)
	}
	_ = logs
}

func TestLogRepo_SumUsedToken(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "sumtoken-user", "sumtoken@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-sumtoken")
	RecordLog(u.Id, LogTypeTopup, "token sum test")

	total := SumUsedToken(LogTypeTopup, 0, 0, "", "", "")
	_ = total // just verify no panic
}

func TestLogRepo_GetTokenLogsInternal(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "toklog-user", "toklog@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-toklog")
	tok := &Token{UserId: u.Id, TenantId: "tenant-toklog", Name: "toklog-tok", Key: common.GetRandomString(48), Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1}
	if err := tok.Insert(); err != nil {
		t.Fatalf("Insert token: %v", err)
	}

	logs, total, err := GetTokenLogsInternal(tok.Id, 0, 10)
	if err != nil {
		t.Fatalf("GetTokenLogsInternal: %v", err)
	}
	_ = logs
	_ = total
}

func TestLogRepo_GetUserLogStatInternal(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "logstat-user", "logstat@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-logstat")
	RecordLog(u.Id, LogTypeTopup, "stat test")

	entries, err := GetUserLogStatInternal(u.Id, "day")
	if err != nil {
		t.Fatalf("GetUserLogStatInternal: %v", err)
	}
	_ = entries
}

// ─── User — daily quota ───────────────────────────────────────────────────────

func TestUserRepo_GetUserDailyQuotaInfo(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "daily-quota-user", "dailyq@test.local", common.RoleCommonUser, common.UserStatusEnabled, "tenant-dailyq")

	info, err := GetUserDailyQuotaInfo(u.Id)
	if err != nil {
		t.Fatalf("GetUserDailyQuotaInfo: %v", err)
	}
	_ = info
}

func TestUserRepo_GetRootUser(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	// Seed a root user
	u := &User{
		Username:    "root-user",
		DisplayName: "Root",
		Email:       "root@test.local",
		Role:        common.RoleRootUser,
		Status:      common.UserStatusEnabled,
		TenantId:    "default",
	}
	if err := u.Insert(); err != nil {
		t.Fatalf("Insert root: %v", err)
	}

	root := GetRootUser()
	if root == nil {
		t.Error("GetRootUser returned nil when root user exists")
	}
}

func TestUserRepo_RootUserExists(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	// Before seeding
	before := RootUserExists()

	u := &User{
		Username:    "rootexist-user",
		DisplayName: "Root Exist",
		Email:       "rootexist@test.local",
		Role:        common.RoleRootUser,
		Status:      common.UserStatusEnabled,
		TenantId:    "default",
	}
	if err := u.Insert(); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	after := RootUserExists()
	if before && !after {
		t.Error("RootUserExists logic inconsistency")
	}
	if !after {
		t.Error("RootUserExists should be true after seeding root")
	}
}
