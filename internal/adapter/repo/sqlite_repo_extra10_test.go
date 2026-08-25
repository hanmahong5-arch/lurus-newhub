package repo

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// ─── GetUserQuota / GetUserGroup / GetUsernameById (fromDB path) ─────────────

func TestGetUserQuota_FromDB(t *testing.T) {
	setupSQLiteDB(t)

	u := &User{Username: "quota-db-user", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, TenantId: "default", Quota: 5000}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	quota, err := GetUserQuota(u.Id, true)
	if err != nil {
		t.Fatalf("GetUserQuota fromDB: %v", err)
	}
	if quota != 5000 {
		t.Errorf("expected quota=5000, got %d", quota)
	}
}

func TestGetUserQuota_RedisDisabled(t *testing.T) {
	setupSQLiteDB(t)

	u := &User{Username: "quota-redis-off-user", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, TenantId: "default", Quota: 2000}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Redis disabled, fromDB=false → falls through to DB
	quota, err := GetUserQuota(u.Id, false)
	if err != nil {
		t.Fatalf("GetUserQuota fromDB=false: %v", err)
	}
	if quota != 2000 {
		t.Errorf("expected quota=2000, got %d", quota)
	}
}

func TestGetUserGroup_FromDB(t *testing.T) {
	setupSQLiteDB(t)

	u := &User{Username: "group-db-user", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, TenantId: "default", Group: "vip"}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	group, err := GetUserGroup(u.Id, true)
	if err != nil {
		t.Fatalf("GetUserGroup fromDB: %v", err)
	}
	if group != "vip" {
		t.Errorf("expected group=vip, got %q", group)
	}
}

func TestGetUserGroup_RedisDisabled(t *testing.T) {
	setupSQLiteDB(t)

	u := &User{Username: "group-redis-off", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, TenantId: "default", Group: "pro"}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	group, err := GetUserGroup(u.Id, false)
	if err != nil {
		t.Fatalf("GetUserGroup fromDB=false: %v", err)
	}
	if group != "pro" {
		t.Errorf("expected group=pro, got %q", group)
	}
}

func TestGetUsernameById_FromDB(t *testing.T) {
	setupSQLiteDB(t)

	u := &User{Username: "username-db-lookup", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, TenantId: "default"}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	name, err := GetUsernameById(u.Id, true)
	if err != nil {
		t.Fatalf("GetUsernameById fromDB: %v", err)
	}
	if name != "username-db-lookup" {
		t.Errorf("expected username 'username-db-lookup', got %q", name)
	}
}

func TestGetUsernameById_RedisDisabled(t *testing.T) {
	setupSQLiteDB(t)

	u := &User{Username: "username-redis-off-lookup", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, TenantId: "default"}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	name, err := GetUsernameById(u.Id, false)
	if err != nil {
		t.Fatalf("GetUsernameById fromDB=false: %v", err)
	}
	if name != "username-redis-off-lookup" {
		t.Errorf("expected username 'username-redis-off-lookup', got %q", name)
	}
}

// ─── GetLogByKey ─────────────────────────────────────────────────────────────

func TestGetLogByKey_JoinPath(t *testing.T) {
	setupSQLiteDB(t)

	// seed user
	u := &User{Username: "log-key-user", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, TenantId: "default"}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	// seed token (key without sk- prefix)
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, -1)

	// seed log linked to token
	_ = seedLog(t, u.Id, LogTypeSystem, "gpt-4", "default")

	// GetLogByKey — exercises the JOIN path (no LOG_SQL_DSN env)
	logs, err := GetLogByKey(tok.Key)
	if err != nil {
		t.Fatalf("GetLogByKey: %v", err)
	}
	_ = logs // may be empty; test that no panic/error on valid key
}

func TestGetLogByKey_WithSkPrefix(t *testing.T) {
	setupSQLiteDB(t)

	u := &User{Username: "log-key-sk-user", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, TenantId: "default"}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, -1)

	// Pass with sk- prefix — TrimPrefix should handle it
	_, err := GetLogByKey("sk-" + tok.Key)
	if err != nil {
		t.Fatalf("GetLogByKey with sk- prefix: %v", err)
	}
}

func TestGetLogByKey_NonExistentKey(t *testing.T) {
	setupSQLiteDB(t)

	// Non-existent key — should return empty slice or error, not panic
	logs, err := GetLogByKey("nonexistent-key-xyz")
	// JOIN path: returns empty slice with no error
	if err != nil {
		t.Logf("GetLogByKey nonexistent: %v (acceptable)", err)
	}
	_ = logs
}

// ─── Token.Update / Token.Delete (error paths via invalid ID) ─────────────────

func TestToken_Update_ValidToken(t *testing.T) {
	setupSQLiteDB(t)

	u := &User{Username: "tok-update-user", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, TenantId: "default"}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	tok := seedToken(t, u.Id, common.TokenStatusEnabled, false, 1000, -1)

	// Update name and status
	tok.Name = "updated-name"
	tok.Status = common.TokenStatusDisabled
	if err := tok.Update(); err != nil {
		t.Fatalf("Token.Update: %v", err)
	}

	var loaded Token
	DB.First(&loaded, tok.Id)
	if loaded.Name != "updated-name" {
		t.Errorf("name not updated: got %q", loaded.Name)
	}
	if loaded.Status != common.TokenStatusDisabled {
		t.Errorf("status not updated: got %d", loaded.Status)
	}
}

func TestToken_Delete_ValidToken(t *testing.T) {
	setupSQLiteDB(t)

	u := &User{Username: "tok-delete-user", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, TenantId: "default"}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	tok := seedToken(t, u.Id, common.TokenStatusEnabled, false, 500, -1)

	if err := tok.Delete(); err != nil {
		t.Fatalf("Token.Delete: %v", err)
	}

	// Verify gone
	var count int64
	DB.Model(&Token{}).Where("id = ?", tok.Id).Count(&count)
	if count != 0 {
		t.Errorf("expected token deleted, but still exists")
	}
}

// ─── GetTokenByKey (DB path) ─────────────────────────────────────────────────

func TestGetTokenByKey_FromDB(t *testing.T) {
	setupSQLiteDB(t)

	u := &User{Username: "tokbykey-user", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, TenantId: "default"}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, -1)

	found, err := GetTokenByKey(tok.Key, true)
	if err != nil {
		t.Fatalf("GetTokenByKey fromDB: %v", err)
	}
	if found == nil || found.Id != tok.Id {
		t.Errorf("expected token id %d, got %v", tok.Id, found)
	}
}

func TestGetTokenByKey_RedisDisabledFallsToDb(t *testing.T) {
	setupSQLiteDB(t)

	u := &User{Username: "tokbykey-redis-off", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, TenantId: "default"}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, -1)

	found, err := GetTokenByKey(tok.Key, false)
	if err != nil {
		t.Fatalf("GetTokenByKey fromDB=false: %v", err)
	}
	if found == nil || found.Id != tok.Id {
		t.Errorf("expected token id %d, got %v", tok.Id, found)
	}
}

// ─── UpdateChannelUsedQuota (BatchUpdate disabled path) ───────────────────────

func TestUpdateChannelUsedQuota_DirectPath(t *testing.T) {
	setupSQLiteDB(t)
	common.BatchUpdateEnabled = false

	ch := seedChannelExtra4(t, "channel-used-quota-direct", "default")

	// Initial used_quota is 0
	UpdateChannelUsedQuota(ch.Id, 200)

	var loaded Channel
	DB.First(&loaded, ch.Id)
	if loaded.UsedQuota < 200 {
		t.Errorf("expected UsedQuota>=200, got %d", loaded.UsedQuota)
	}
}

// ─── Token.SelectUpdate ─────────────────────────────────────────────────────

func TestToken_SelectUpdate_ValidToken(t *testing.T) {
	setupSQLiteDB(t)

	u := &User{Username: "selectupdate-user", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, TenantId: "default"}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	tok := seedToken(t, u.Id, common.TokenStatusEnabled, false, 1000, -1)
	// SelectUpdate only writes accessed_time + status
	tok.Status = common.TokenStatusDisabled

	if err := tok.SelectUpdate(); err != nil {
		t.Fatalf("Token.SelectUpdate: %v", err)
	}

	var loaded Token
	DB.First(&loaded, tok.Id)
	if loaded.Status != common.TokenStatusDisabled {
		t.Errorf("status not updated via SelectUpdate: got %d", loaded.Status)
	}
}

// ─── IncreaseTokenQuota / DecreaseTokenQuota ──────────────────────────────────

func TestIncreaseTokenQuota_FromDB(t *testing.T) {
	setupSQLiteDB(t)

	u := &User{Username: "inc-quota-user", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, TenantId: "default"}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	tok := seedToken(t, u.Id, common.TokenStatusEnabled, false, 1000, -1)

	// IncreaseTokenQuota(id, key, quota)
	if err := IncreaseTokenQuota(tok.Id, tok.Key, 500); err != nil {
		t.Fatalf("IncreaseTokenQuota: %v", err)
	}

	var loaded Token
	DB.First(&loaded, tok.Id)
	if loaded.RemainQuota != 1500 {
		t.Errorf("expected RemainQuota=1500, got %d", loaded.RemainQuota)
	}
}

func TestDecreaseTokenQuota_FromDB(t *testing.T) {
	setupSQLiteDB(t)

	u := &User{Username: "dec-quota-user", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, TenantId: "default"}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	tok := seedToken(t, u.Id, common.TokenStatusEnabled, false, 1000, -1)

	if err := DecreaseTokenQuota(tok.Id, tok.Key, 300); err != nil {
		t.Fatalf("DecreaseTokenQuota: %v", err)
	}

	var loaded Token
	DB.First(&loaded, tok.Id)
	if loaded.RemainQuota != 700 {
		t.Errorf("expected RemainQuota=700, got %d", loaded.RemainQuota)
	}
}

// ─── GetUserSetting (DB path) ────────────────────────────────────────────────

func TestGetUserSetting_FromDB(t *testing.T) {
	setupSQLiteDB(t)

	u := &User{Username: "usersetting-db-user", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, TenantId: "default"}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	setting, err := GetUserSetting(u.Id, true)
	if err != nil {
		t.Fatalf("GetUserSetting fromDB: %v", err)
	}
	_ = setting
}

func TestGetUserSetting_RedisDisabled(t *testing.T) {
	setupSQLiteDB(t)

	u := &User{Username: "usersetting-redis-off", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, TenantId: "default"}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	setting, err := GetUserSetting(u.Id, false)
	if err != nil {
		t.Fatalf("GetUserSetting fromDB=false: %v", err)
	}
	_ = setting
}

// ─── BatchDeleteTokens (additional path) ─────────────────────────────────────

func TestBatchDeleteTokens_WithUser(t *testing.T) {
	setupSQLiteDB(t)

	u := &User{Username: "batchdel-tok-user2", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, TenantId: "default"}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	tok1 := seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, -1)
	tok2 := seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, -1)

	ids := []int{tok1.Id, tok2.Id}
	_, err := BatchDeleteTokens(ids, u.Id)
	if err != nil {
		t.Fatalf("BatchDeleteTokens: %v", err)
	}

	var count int64
	DB.Model(&Token{}).Where("id IN ?", ids).Count(&count)
	if count != 0 {
		t.Errorf("expected tokens deleted, got %d remaining", count)
	}
}

// ─── CreateUserFromIDPClaims paths ───────────────────────────────────────

func TestCreateUserFromIDPClaims_ExistingMapping(t *testing.T) {
	setupSQLiteDB(t)

	// seed a user and mapping first
	u := &User{Username: "zit-claims-existing", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, TenantId: "default"}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	mapping, err := CreateUserMapping(u.Id, "sub-existing-001", "default",
		"existing@ex.com", "Existing", "existing")
	if err != nil {
		t.Fatalf("CreateUserMapping: %v", err)
	}
	_ = mapping

	claims := &OIDCUserClaims{
		Sub:               "sub-existing-001",
		Email:             "existing@ex.com",
		Name:              "Existing",
		PreferredUsername: "existing",
	}

	// Should return existing user without creating new
	user, m, err := CreateUserFromIDPClaims(claims, "default")
	if err != nil {
		t.Fatalf("CreateUserFromIDPClaims existing: %v", err)
	}
	if user.Id != u.Id {
		t.Errorf("expected user id %d, got %d", u.Id, user.Id)
	}
	_ = m
}

func TestCreateUserFromIDPClaims_EmailFallback(t *testing.T) {
	setupSQLiteDB(t)

	// Seed a user without any mapping (simulates pre-Zitadel user)
	existing := &User{
		Username: "pre-zitadel-user",
		Email:    "pre-zitadel@ex.com",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		TenantId: "default",
		Group:    "default",
	}
	if err := DB.Create(existing).Error; err != nil {
		t.Fatalf("create pre-zitadel user: %v", err)
	}

	claims := &OIDCUserClaims{
		Sub:   "sub-email-fallback-001",
		Email: "pre-zitadel@ex.com",
		// The fallback only trusts a verified address. This test predates that
		// rule and left the field at its zero value; it means to cover the
		// legitimate same-tenant, non-privileged link, so it asserts the
		// verified case. The unverified case is locked separately in
		// user_mapping_email_fallback_test.go.
		EmailVerified:     true,
		Name:              "Pre Zitadel",
		PreferredUsername: "prezit",
	}

	user, m, err := CreateUserFromIDPClaims(claims, "default")
	if err != nil {
		t.Fatalf("CreateUserFromIDPClaims email fallback: %v", err)
	}
	if user.Id != existing.Id {
		t.Errorf("expected existing user id %d, got %d", existing.Id, user.Id)
	}
	_ = m
}
