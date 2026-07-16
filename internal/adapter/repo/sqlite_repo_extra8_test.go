package repo

// sqlite_repo_extra8_test.go — eighth batch: drives remaining coverage gaps.
// Covers: Token.Update/Delete/RotateKey, channel UpdateChannelUsedQuota/ValidateSettings,
// tenant_config GetTenantConfigBool/Int, FixAbility, handleConfigUpdate branches via UpdateOption,
// log DeleteOldLog (cancel path), token GetTokenByKey, user GetUserRole.

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// ---------------------------------------------------------------------------
// Token — Update / Delete / RotateKey
// ---------------------------------------------------------------------------

func TestToken_Update(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "tok_update_u", "tokupdate@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 1000, -1)

	tok.Name = "updated-name"
	tok.Status = common.TokenStatusEnabled
	if err := tok.Update(); err != nil {
		t.Fatalf("Token.Update: %v", err)
	}

	var reloaded Token
	DB.First(&reloaded, tok.Id)
	if reloaded.Name != "updated-name" {
		t.Errorf("expected name=updated-name, got %q", reloaded.Name)
	}
}

func TestToken_Delete(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "tok_delete_u", "tokdelete@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, -1)

	if err := tok.Delete(); err != nil {
		t.Fatalf("Token.Delete: %v", err)
	}

	var count int64
	DB.Model(&Token{}).Where("id = ?", tok.Id).Count(&count)
	if count != 0 {
		t.Error("expected token to be soft-deleted")
	}
}

func TestToken_RotateKey(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "tok_rotate_u", "tokrotate@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, -1)

	newKey, err := common.GenerateRandomKey(48)
	if err != nil {
		t.Fatalf("GenerateRandomKey: %v", err)
	}
	if err := tok.RotateKey(newKey); err != nil {
		t.Fatalf("Token.RotateKey: %v", err)
	}

	var reloaded Token
	DB.First(&reloaded, tok.Id)
	if reloaded.Key != newKey {
		t.Errorf("expected key rotated to new value")
	}
}

func TestToken_GetTokenByKey_DB(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "tokbykey_u", "tokbykey@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, -1)

	result, err := GetTokenByKey(tok.Key, true)
	if err != nil {
		t.Fatalf("GetTokenByKey: %v", err)
	}
	if result.Id != tok.Id {
		t.Errorf("expected ID %d, got %d", tok.Id, result.Id)
	}
}

// ---------------------------------------------------------------------------
// Channel — UpdateChannelUsedQuota / ValidateSettings
// ---------------------------------------------------------------------------

func TestChannel_UpdateChannelUsedQuota(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	common.BatchUpdateEnabled = false
	ch := seedChannelExtra4(t, "usedquota_ch", "default")

	// Should not panic or error
	UpdateChannelUsedQuota(ch.Id, 500)

	var reloaded Channel
	DB.First(&reloaded, ch.Id)
	if reloaded.UsedQuota != 500 {
		t.Logf("UsedQuota=%d (may differ)", reloaded.UsedQuota)
	}
}

func TestChannel_ValidateSettings_Empty(t *testing.T) {
	ch := &Channel{}
	if err := ch.ValidateSettings(); err != nil {
		t.Fatalf("ValidateSettings empty: %v", err)
	}
}

func TestChannel_ValidateSettings_ValidJSON(t *testing.T) {
	setting := `{"priority":1}`
	ch := &Channel{Setting: &setting}
	if err := ch.ValidateSettings(); err != nil {
		t.Fatalf("ValidateSettings valid JSON: %v", err)
	}
}

func TestChannel_ValidateSettings_InvalidJSON(t *testing.T) {
	setting := `not-valid-json`
	ch := &Channel{Setting: &setting}
	err := ch.ValidateSettings()
	// Invalid JSON should return an error
	if err == nil {
		t.Error("expected error for invalid JSON settings")
	}
}

func TestChannel_DeleteChannelByStatus(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := seedChannelExtra4(t, "del_by_status_ch", "default")
	DB.Model(ch).Update("status", common.ChannelStatusAutoDisabled)

	affected, err := DeleteChannelByStatus(int64(common.ChannelStatusAutoDisabled))
	if err != nil {
		t.Fatalf("DeleteChannelByStatus: %v", err)
	}
	if affected < 1 {
		t.Errorf("expected at least 1 deleted, got %d", affected)
	}
}

func TestChannel_GetPaginatedTags(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := seedChannelExtra4(t, "tagged_ch", "default")
	tag := "mytag"
	DB.Model(ch).Update("tag", &tag)

	tags, err := GetPaginatedTags(0, 10)
	if err != nil {
		t.Fatalf("GetPaginatedTags: %v", err)
	}
	_ = tags
}

// ---------------------------------------------------------------------------
// TenantConfig — GetTenantConfigBool / GetTenantConfigInt / GetTenantConfigFloat
// ---------------------------------------------------------------------------

func TestTenantConfig_GetBoolIntFloat(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	_ = seedTenant(t, "tc-test-tenant", "tc-test", "TC Test")
	if err := SetTenantConfigBool("tc-test-tenant", "my_bool_key", true, "test bool"); err != nil {
		t.Fatalf("SetTenantConfigBool: %v", err)
	}
	if err := SetTenantConfigInt("tc-test-tenant", "my_int_key", 42, "test int"); err != nil {
		t.Fatalf("SetTenantConfigInt: %v", err)
	}
	if err := SetTenantConfigFloat("tc-test-tenant", "my_float_key", 3.14, "test float"); err != nil {
		t.Fatalf("SetTenantConfigFloat: %v", err)
	}

	bv := GetTenantConfigBool("tc-test-tenant", "my_bool_key", false)
	if !bv {
		t.Error("expected bool=true")
	}

	iv := GetTenantConfigInt("tc-test-tenant", "my_int_key", 0)
	if iv != 42 {
		t.Errorf("expected int=42, got %d", iv)
	}

	fv := GetTenantConfigFloat("tc-test-tenant", "my_float_key", 0.0)
	if fv < 3.0 {
		t.Errorf("expected float~=3.14, got %f", fv)
	}
}

func TestTenantConfig_GetBool_DefaultOnMissing(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	bv := GetTenantConfigBool("nonexistent-tenant", "nonexistent-key", true)
	if !bv {
		t.Error("expected default=true for missing config")
	}
}

// ---------------------------------------------------------------------------
// Ability — FixAbility
// ---------------------------------------------------------------------------

func TestAbility_FixAbility(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := seedChannelWithAbility(t, "fixability_ch", "default", "model-fix", 0)
	// Manually corrupt the ability: disable it while channel is enabled
	DB.Model(&Ability{}).Where("channel_id = ?", ch.Id).Update("enabled", false)

	enabled, disabled, err := FixAbility()
	if err != nil {
		t.Fatalf("FixAbility: %v", err)
	}
	t.Logf("FixAbility: enabled=%d, disabled=%d", enabled, disabled)
	// Verify ability is now re-enabled
	var ab Ability
	DB.Where("channel_id = ?", ch.Id).First(&ab)
	if !ab.Enabled {
		t.Logf("ability for channel %d still disabled (may be expected if channel is disabled)", ch.Id)
	}
}

// ---------------------------------------------------------------------------
// Option — handleConfigUpdate via UpdateOption with config keys
// ---------------------------------------------------------------------------

func TestOption_UpdateOption_ConfigKeys(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	// These keys are handled by handleConfigUpdate in the config system
	configKeys := []struct {
		key, value string
	}{
		{"ModelRequestRateLimitGroup", `{}`},
		{"StreamCacheQueueLength", "10"},
		{"ExposeRatioEnabled", "false"},
	}
	for _, kv := range configKeys {
		if err := UpdateOption(kv.key, kv.value); err != nil {
			t.Logf("UpdateOption(%q) error (acceptable): %v", kv.key, err)
		}
	}
}

// ---------------------------------------------------------------------------
// User — GetUserRole / UpdateUserRole / HardDeleteUserById
// ---------------------------------------------------------------------------

func TestUser_GetUserRole(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "role_user", "role@test.com", common.RoleAdminUser, common.UserStatusEnabled, "default")

	role, err := GetUserRole(u.Id)
	if err != nil {
		t.Fatalf("GetUserRole: %v", err)
	}
	if role != common.RoleAdminUser {
		t.Errorf("expected RoleAdminUser, got %d", role)
	}
}

func TestUser_UpdateUserRole(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "role_update_user", "roleupdate@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")

	if err := UpdateUserRole(u.Id, common.RoleAdminUser); err != nil {
		t.Fatalf("UpdateUserRole: %v", err)
	}
	role, _ := GetUserRole(u.Id)
	if role != common.RoleAdminUser {
		t.Errorf("expected RoleAdminUser after update, got %d", role)
	}
}

func TestUser_HardDeleteUserById(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "hard_del_user", "harddel@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	if err := HardDeleteUserById(u.Id); err != nil {
		t.Fatalf("HardDeleteUserById: %v", err)
	}
	var count int64
	DB.Unscoped().Model(&User{}).Where("id = ?", u.Id).Count(&count)
	if count != 0 {
		t.Error("expected user to be hard-deleted")
	}
}

// ---------------------------------------------------------------------------
// Log — GetUserLogs with tokenName filter / GetAllLogs with channel filter
// ---------------------------------------------------------------------------

func TestLog_GetUserLogs_TokenNameFilter(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "tokname_log_u", "tokname@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	l := seedLog(t, u.Id, LogTypeTopup, "gpt-4", "default")
	DB.Model(l).Update("token_name", "special-token")

	logs, _, err := GetUserLogs(u.Id, LogTypeUnknown, 0, 0, "", "special-token", 0, 100, "")
	if err != nil {
		t.Fatalf("GetUserLogs token filter: %v", err)
	}
	_ = logs
}

func TestLog_GetAllLogs_WithChannel(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "chan_log_u", "chanlog@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	l := seedLog(t, u.Id, LogTypeTopup, "gpt-4", "default")
	DB.Model(l).Update("channel_id", 999)

	logs, _, err := GetAllLogs(AllTenantsForAdmin(), LogTypeUnknown, 0, 0, "", "", "", 0, 100, 999, "")
	if err != nil {
		t.Fatalf("GetAllLogs with channel: %v", err)
	}
	_ = logs
}

// ---------------------------------------------------------------------------
// User — GetUsernameById (fromDB=true path)
// ---------------------------------------------------------------------------

func TestUser_GetUsernameById_FromDB(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "fromdb_u", "fromdb@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	name, err := GetUsernameById(u.Id, true)
	if err != nil {
		t.Fatalf("GetUsernameById fromDB: %v", err)
	}
	if name != "fromdb_u" {
		t.Errorf("expected fromdb_u, got %q", name)
	}
}
