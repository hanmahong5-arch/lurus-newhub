package repo

// sqlite_repo_extra4_test.go — fourth batch of unit tests for repo package.
// Covers: user quota/group/setting DB paths, token validation paths,
// IncreaseTokenQuota/DecreaseTokenQuota, channel Update, log GetAllLogs/GetUserLogs,
// option UpdateOption/AllOption, user IncreaseUserQuota/DecreaseUserQuota.

import (
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// ---------------------------------------------------------------------------
// User — quota / group / setting DB paths (RedisEnabled=false forces DB read)
// ---------------------------------------------------------------------------

func TestUser_GetUserQuota_FromDB(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "quota_user", "quota@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	DB.Model(u).Update("quota", 99999)

	quota, err := GetUserQuota(u.Id, true)
	if err != nil {
		t.Fatalf("GetUserQuota: %v", err)
	}
	if quota != 99999 {
		t.Errorf("expected quota=99999, got %d", quota)
	}
}

func TestUser_GetUserQuota_NotFound(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	// Non-existent user — GORM Find returns 0, no error
	quota, err := GetUserQuota(99999, true)
	if err != nil {
		t.Fatalf("unexpected error for missing user: %v", err)
	}
	if quota != 0 {
		t.Errorf("expected quota=0 for missing user, got %d", quota)
	}
}

func TestUser_GetUserGroup_FromDB(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "group_user", "group@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	DB.Model(u).Update("group", "premium")

	group, err := GetUserGroup(u.Id, true)
	if err != nil {
		t.Fatalf("GetUserGroup: %v", err)
	}
	if group != "premium" {
		t.Errorf("expected group=premium, got %q", group)
	}
}

func TestUser_GetUserSetting_FromDB(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "setting_user", "setting@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	// Empty setting should deserialize to zero-value UserSetting without error
	setting, err := GetUserSetting(u.Id, true)
	if err != nil {
		t.Fatalf("GetUserSetting: %v", err)
	}
	// Default setting should have RecordIpLog=false
	if setting.RecordIpLog {
		t.Error("expected RecordIpLog=false for new user")
	}
}

func TestUser_GetUserUsedQuota(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "usedquota_user", "usedquota@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	DB.Model(u).Update("used_quota", 500)

	q, err := GetUserUsedQuota(u.Id)
	if err != nil {
		t.Fatalf("GetUserUsedQuota: %v", err)
	}
	if q != 500 {
		t.Errorf("expected 500, got %d", q)
	}
}

func TestUser_GetUserEmail(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "email_user", "myemail@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	email, err := GetUserEmail(u.Id)
	if err != nil {
		t.Fatalf("GetUserEmail: %v", err)
	}
	if email != "myemail@test.com" {
		t.Errorf("expected myemail@test.com, got %q", email)
	}
}

func TestUser_IncreaseUserQuota_DirectDB(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	common.BatchUpdateEnabled = false
	u := seedUser(t, "incquota_user", "incquota@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	DB.Model(u).Update("quota", 1000)

	if err := IncreaseUserQuota(u.Id, 500, true); err != nil {
		t.Fatalf("IncreaseUserQuota: %v", err)
	}
	var q int
	DB.Model(&User{}).Where("id = ?", u.Id).Select("quota").Scan(&q)
	if q != 1500 {
		t.Errorf("expected quota=1500, got %d", q)
	}
}

func TestUser_IncreaseUserQuota_NegativeRejected(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "negquota_user", "negquota@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	if err := IncreaseUserQuota(u.Id, -1, true); err == nil {
		t.Error("expected error for negative quota")
	}
}

func TestUser_DecreaseUserQuota_DirectDB(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	common.BatchUpdateEnabled = false
	u := seedUser(t, "decquota_user", "decquota@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	DB.Model(u).Update("quota", 2000)

	if err := DecreaseUserQuota(u.Id, 300); err != nil {
		t.Fatalf("DecreaseUserQuota: %v", err)
	}
	var q int
	DB.Model(&User{}).Where("id = ?", u.Id).Select("quota").Scan(&q)
	if q != 1700 {
		t.Errorf("expected quota=1700, got %d", q)
	}
}

func TestUser_DeltaUpdateUserQuota(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	common.BatchUpdateEnabled = false
	u := seedUser(t, "delta_user", "delta@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	DB.Model(u).Update("quota", 1000)

	if err := DeltaUpdateUserQuota(u.Id, 200); err != nil {
		t.Fatalf("DeltaUpdateUserQuota positive: %v", err)
	}
	var q int
	DB.Model(&User{}).Where("id = ?", u.Id).Select("quota").Scan(&q)
	if q != 1200 {
		t.Errorf("expected quota=1200, got %d", q)
	}

	if err := DeltaUpdateUserQuota(u.Id, -400); err != nil {
		t.Fatalf("DeltaUpdateUserQuota negative: %v", err)
	}
	DB.Model(&User{}).Where("id = ?", u.Id).Select("quota").Scan(&q)
	if q != 800 {
		t.Errorf("expected quota=800, got %d", q)
	}
}

func TestUser_DeltaUpdateUserQuota_Zero(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "zero_delta", "zerodelta@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	DB.Model(u).Update("quota", 1000)

	if err := DeltaUpdateUserQuota(u.Id, 0); err != nil {
		t.Fatalf("DeltaUpdateUserQuota zero: %v", err)
	}
}

func TestUser_GetRootUser(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	_ = seedUser(t, "rootuser", "root@test.com", common.RoleRootUser, common.UserStatusEnabled, "default")
	root := GetRootUser()
	if root == nil {
		t.Fatal("expected root user")
	}
	if root.Role != common.RoleRootUser {
		t.Errorf("expected RoleRootUser, got %d", root.Role)
	}
}

func TestUser_UpdateUserUsedQuotaAndRequestCount(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	common.BatchUpdateEnabled = false
	u := seedUser(t, "usedcount_user", "usedcount@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")

	UpdateUserUsedQuotaAndRequestCount(u.Id, 300)

	var used int
	DB.Model(&User{}).Where("id = ?", u.Id).Select("used_quota").Scan(&used)
	if used != 300 {
		t.Errorf("expected used_quota=300, got %d", used)
	}
}

// ---------------------------------------------------------------------------
// Token — ValidateUserToken paths (no Redis)
// ---------------------------------------------------------------------------

func seedToken(t *testing.T, userID int, status int, unlimitedQuota bool, remainQuota int, expiredTime int64) *Token {
	t.Helper()
	key, err := common.GenerateRandomKey(48)
	if err != nil {
		t.Fatalf("GenerateRandomKey: %v", err)
	}
	tok := &Token{
		UserId:         userID,
		TenantId:       "default",
		Key:            key,
		Name:           "test-token",
		Status:         status,
		UnlimitedQuota: unlimitedQuota,
		RemainQuota:    remainQuota,
		ExpiredTime:    expiredTime,
	}
	if err := DB.Create(tok).Error; err != nil {
		t.Fatalf("seedToken: %v", err)
	}
	return tok
}

func TestToken_ValidateUserToken_EmptyKey(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	_, err := ValidateUserToken("")
	if err == nil {
		t.Error("expected error for empty key")
	}
}

func TestToken_ValidateUserToken_Valid(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "tok_valid", "tokvalid@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 1000, -1)

	// ValidateUserToken queries the key as-is; keys are stored without "sk-" prefix.
	result, err := ValidateUserToken(tok.Key)
	if err != nil {
		t.Fatalf("ValidateUserToken valid: %v", err)
	}
	if result.Id != tok.Id {
		t.Errorf("expected token ID %d, got %d", tok.Id, result.Id)
	}
}

func TestToken_ValidateUserToken_Exhausted_DB(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "tok_exhausted2", "tokex2@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusExhausted, false, 0, -1)

	_, err := ValidateUserToken(tok.Key)
	if err == nil {
		t.Error("expected error for exhausted token")
	}
}

func TestToken_ValidateUserToken_Expired_DB(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "tok_expired2", "tokexp2@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusExpired, false, 1000, -1)

	_, err := ValidateUserToken(tok.Key)
	if err == nil {
		t.Error("expected error for expired token")
	}
}

func TestToken_ValidateUserToken_ExpiredByTime(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "tok_exptime", "tokexptime@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	// ExpiredTime in the past — should trigger expired path
	pastTs := common.GetTimestamp() - 10000
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 1000, pastTs)

	_, err := ValidateUserToken(tok.Key)
	if err == nil {
		t.Error("expected error for time-expired token")
	}
	if !strings.Contains(err.Error(), "过期") {
		t.Errorf("expected 过期 in error, got: %v", err)
	}
}

func TestToken_ValidateUserToken_ZeroQuotaUnlimited(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "tok_unlimited", "tokunl@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	// UnlimitedQuota=true, RemainQuota=0 should still pass
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, -1)

	result, err := ValidateUserToken(tok.Key)
	if err != nil {
		t.Fatalf("expected pass for unlimited quota token: %v", err)
	}
	if result.Id != tok.Id {
		t.Errorf("expected token ID %d", tok.Id)
	}
}

func TestToken_ValidateUserToken_ZeroQuotaLimited(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "tok_limited", "toklim@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	// UnlimitedQuota=false, RemainQuota=0 should fail
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, false, 0, -1)

	_, err := ValidateUserToken(tok.Key)
	if err == nil {
		t.Error("expected error for limited quota=0 token")
	}
}

func TestToken_ValidateUserToken_NotFound(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	_, err := ValidateUserToken("nonexistentkey123456789012345678901234567890")
	if err == nil {
		t.Error("expected error for non-existent key")
	}
}

func TestToken_IncreaseTokenQuota(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	common.BatchUpdateEnabled = false
	u := seedUser(t, "inctok_user", "inctok@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, false, 1000, -1)
	DB.Model(tok).Update("used_quota", 500)

	if err := IncreaseTokenQuota(tok.Id, tok.Key, 200); err != nil {
		t.Fatalf("IncreaseTokenQuota: %v", err)
	}

	var remain int
	DB.Model(&Token{}).Where("id = ?", tok.Id).Select("remain_quota").Scan(&remain)
	if remain != 1200 {
		t.Errorf("expected remain_quota=1200, got %d", remain)
	}
}

func TestToken_IncreaseTokenQuota_Negative(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	if err := IncreaseTokenQuota(1, "anykey", -1); err == nil {
		t.Error("expected error for negative quota")
	}
}

func TestToken_DecreaseTokenQuota(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	common.BatchUpdateEnabled = false
	u := seedUser(t, "dectok_user", "dectok@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, false, 1000, -1)

	if err := DecreaseTokenQuota(tok.Id, tok.Key, 300); err != nil {
		t.Fatalf("DecreaseTokenQuota: %v", err)
	}

	var remain int
	DB.Model(&Token{}).Where("id = ?", tok.Id).Select("remain_quota").Scan(&remain)
	if remain != 700 {
		t.Errorf("expected remain_quota=700, got %d", remain)
	}
}

func TestToken_DecreaseTokenQuota_Negative(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	if err := DecreaseTokenQuota(1, "anykey", -1); err == nil {
		t.Error("expected error for negative quota")
	}
}

func TestToken_CountUserTokens_Extra4(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "counttok_user", "counttok@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, -1)
	seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, -1)

	count, err := CountUserTokens(u.Id)
	if err != nil {
		t.Fatalf("CountUserTokens: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 tokens, got %d", count)
	}
}

func TestToken_GetUserTokensByTenant(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "bytenant_user", "bytenant@test.com", common.RoleCommonUser, common.UserStatusEnabled, "tenant-a")
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, -1)
	// Update tenant_id on the token directly
	DB.Model(tok).Update("tenant_id", "tenant-a")

	tokens, err := GetUserTokensByTenant(u.Id, "tenant-a", 0, 10)
	if err != nil {
		t.Fatalf("GetUserTokensByTenant: %v", err)
	}
	if len(tokens) == 0 {
		t.Error("expected at least one token")
	}
}

func TestToken_CountUserTokensByTenant(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "cnt_bytenant", "cntbt@test.com", common.RoleCommonUser, common.UserStatusEnabled, "tenant-b")
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, -1)
	DB.Model(tok).Update("tenant_id", "tenant-b")

	count, err := CountUserTokensByTenant(u.Id, "tenant-b")
	if err != nil {
		t.Fatalf("CountUserTokensByTenant: %v", err)
	}
	if count < 1 {
		t.Errorf("expected count >= 1, got %d", count)
	}
}

func TestToken_GetTokenByIds(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "byids_user", "byids@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, -1)

	result, err := GetTokenByIds(tok.Id, u.Id)
	if err != nil {
		t.Fatalf("GetTokenByIds: %v", err)
	}
	if result.Id != tok.Id {
		t.Errorf("expected ID %d, got %d", tok.Id, result.Id)
	}
}

func TestToken_GetTokenByIds_Zeros(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	_, err := GetTokenByIds(0, 0)
	if err == nil {
		t.Error("expected error for zero IDs")
	}
}

func TestToken_GetTokenById(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "byid_user", "byid@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, -1)

	result, err := GetTokenById(tok.Id)
	if err != nil {
		t.Fatalf("GetTokenById: %v", err)
	}
	if result.Id != tok.Id {
		t.Errorf("expected ID %d, got %d", tok.Id, result.Id)
	}
}

func TestToken_GetTokenById_Zero(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	_, err := GetTokenById(0)
	if err == nil {
		t.Error("expected error for zero ID")
	}
}

func TestToken_DeleteTokenById(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "del_tok_user", "deltok@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 0, -1)

	if err := DeleteTokenById(tok.Id, u.Id); err != nil {
		t.Fatalf("DeleteTokenById: %v", err)
	}
	// Verify gone
	var count int64
	DB.Model(&Token{}).Where("id = ?", tok.Id).Count(&count)
	if count != 0 {
		t.Error("expected token to be deleted")
	}
}

func TestToken_DeleteTokenById_Zeros(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	if err := DeleteTokenById(0, 0); err == nil {
		t.Error("expected error for zero IDs")
	}
}

// ---------------------------------------------------------------------------
// Channel — Update + polling lock helpers
// ---------------------------------------------------------------------------

func seedChannelExtra4(t *testing.T, name string, tenantID string) *Channel {
	t.Helper()
	ch := &Channel{
		Name:     name,
		Type:     1,
		Key:      "test-key-" + name,
		Status:   common.ChannelStatusEnabled,
		TenantId: tenantID,
	}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatalf("seedChannel %q: %v", name, err)
	}
	return ch
}

func TestChannel_Update_BasicFields(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := seedChannelExtra4(t, "update_test", "default")
	ch.Name = "updated_name"

	if err := ch.Update(); err != nil {
		t.Fatalf("Channel.Update: %v", err)
	}
	// Reload and verify
	var reloaded Channel
	DB.First(&reloaded, ch.Id)
	if reloaded.Name != "updated_name" {
		t.Errorf("expected name=updated_name, got %q", reloaded.Name)
	}
}

func TestChannel_GetChannelPollingLock(t *testing.T) {
	// No DB needed — tests in-memory sync.Map
	lock1 := GetChannelPollingLock(100)
	lock2 := GetChannelPollingLock(100)
	if lock1 != lock2 {
		t.Error("expected same lock for same channelId")
	}
	lock3 := GetChannelPollingLock(200)
	if lock1 == lock3 {
		t.Error("expected different lock for different channelId")
	}
}

func TestChannel_UpdateResponseTime(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := seedChannelExtra4(t, "resp_time_ch", "default")
	ch.UpdateResponseTime(123)

	var reloaded Channel
	DB.First(&reloaded, ch.Id)
	if reloaded.ResponseTime != 123 {
		t.Errorf("expected ResponseTime=123, got %d", reloaded.ResponseTime)
	}
}

func TestChannel_UpdateBalance(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := seedChannelExtra4(t, "balance_ch", "default")
	ch.UpdateBalance(9.99)

	var reloaded Channel
	DB.First(&reloaded, ch.Id)
	if reloaded.Balance != 9.99 {
		t.Errorf("expected Balance=9.99, got %f", reloaded.Balance)
	}
}

func TestChannel_Delete(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ch := seedChannelExtra4(t, "del_ch", "default")
	if err := ch.Delete(); err != nil {
		t.Fatalf("Channel.Delete: %v", err)
	}
	var count int64
	DB.Model(&Channel{}).Where("id = ?", ch.Id).Count(&count)
	if count != 0 {
		t.Error("expected channel to be deleted")
	}
}

func TestChannel_CleanupChannelPollingLocks(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	// Seed channels, then clean up — should not panic
	seedChannelExtra4(t, "lock_cleanup_ch", "default")
	// Add a lock for a non-existent channel
	GetChannelPollingLock(9999999)

	CleanupChannelPollingLocks()
	// If lock 9999999 was cleaned up, LoadOrStore will create a new one
	newLock := GetChannelPollingLock(9999999)
	if newLock == nil {
		t.Error("expected non-nil lock after cleanup")
	}
}

// ---------------------------------------------------------------------------
// Log — GetAllLogs / GetUserLogs with various filter combinations
// ---------------------------------------------------------------------------

func seedLog(t *testing.T, userID int, logType int, modelName string, tenantID string) *Log {
	t.Helper()
	l := &Log{
		UserId:    userID,
		TenantId:  tenantID,
		Username:  "testuser",
		Type:      logType,
		ModelName: modelName,
		CreatedAt: common.GetTimestamp(),
		Content:   "test log",
	}
	if err := LOG_DB.Create(l).Error; err != nil {
		t.Fatalf("seedLog: %v", err)
	}
	return l
}

func TestLog_GetAllLogs_NoFilter(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "log_user1", "log1@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	seedLog(t, u.Id, LogTypeTopup, "gpt-4", "default")
	seedLog(t, u.Id, LogTypeTopup, "gpt-3.5", "default")

	logs, total, err := GetAllLogs(AllTenantsForAdmin(), LogTypeUnknown, 0, 0, "", "", "", 0, 100, 0, "")
	if err != nil {
		t.Fatalf("GetAllLogs: %v", err)
	}
	if total < 2 {
		t.Errorf("expected total >= 2, got %d", total)
	}
	_ = logs
}

func TestLog_GetAllLogs_ByType(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "log_user2", "log2@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	seedLog(t, u.Id, LogTypeTopup, "gpt-4", "default")
	seedLog(t, u.Id, LogTypeSystem, "gpt-3.5", "default")

	logs, total, err := GetAllLogs(AllTenantsForAdmin(), LogTypeTopup, 0, 0, "", "", "", 0, 100, 0, "")
	if err != nil {
		t.Fatalf("GetAllLogs by type: %v", err)
	}
	for _, log := range logs {
		if log.Type != LogTypeTopup {
			t.Errorf("expected all topup logs, got type=%d", log.Type)
		}
	}
	_ = total
}

func TestLog_GetAllLogs_ByModel(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "log_user3", "log3@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	seedLog(t, u.Id, LogTypeTopup, "gpt-4", "default")
	seedLog(t, u.Id, LogTypeTopup, "claude-3", "default")

	logs, _, err := GetAllLogs(AllTenantsForAdmin(), LogTypeUnknown, 0, 0, "gpt-4", "", "", 0, 100, 0, "")
	if err != nil {
		t.Fatalf("GetAllLogs by model: %v", err)
	}
	for _, log := range logs {
		if !strings.Contains(log.ModelName, "gpt-4") {
			t.Errorf("expected gpt-4 logs only, got model=%q", log.ModelName)
		}
	}
}

func TestLog_GetAllLogs_ByUsername(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "log_user4", "log4@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	l := seedLog(t, u.Id, LogTypeTopup, "gpt-4", "default")
	DB.Model(l).Update("username", "log_user4")

	logs, _, err := GetAllLogs(AllTenantsForAdmin(), LogTypeUnknown, 0, 0, "", "log_user4", "", 0, 100, 0, "")
	if err != nil {
		t.Fatalf("GetAllLogs by username: %v", err)
	}
	_ = logs
}

func TestLog_GetAllLogs_ByTimeRange(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "log_user5", "log5@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	seedLog(t, u.Id, LogTypeTopup, "gpt-4", "default")

	now := common.GetTimestamp()
	logs, _, err := GetAllLogs(AllTenantsForAdmin(), LogTypeUnknown, now-10000, now+10000, "", "", "", 0, 100, 0, "")
	if err != nil {
		t.Fatalf("GetAllLogs by time range: %v", err)
	}
	_ = logs
}

func TestLog_GetUserLogs(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "userlog_user", "userlog@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	seedLog(t, u.Id, LogTypeTopup, "gpt-4", "default")
	seedLog(t, u.Id, LogTypeSystem, "gpt-3.5", "default")

	logs, total, err := GetUserLogs(u.Id, LogTypeUnknown, 0, 0, "", "", 0, 100, "")
	if err != nil {
		t.Fatalf("GetUserLogs: %v", err)
	}
	if total < 2 {
		t.Errorf("expected total >= 2, got %d", total)
	}
	_ = logs
}

func TestLog_GetUserLogs_ByType(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "userlog2_user", "userlog2@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	seedLog(t, u.Id, LogTypeTopup, "gpt-4", "default")
	seedLog(t, u.Id, LogTypeSystem, "gpt-3.5", "default")

	logs, _, err := GetUserLogs(u.Id, LogTypeTopup, 0, 0, "", "", 0, 100, "")
	if err != nil {
		t.Fatalf("GetUserLogs by type: %v", err)
	}
	for _, log := range logs {
		if log.Type != LogTypeTopup {
			t.Errorf("expected topup only, got type=%d", log.Type)
		}
	}
}

func TestLog_GetUserLogs_ModelFilter(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "userlog3_user", "userlog3@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	seedLog(t, u.Id, LogTypeTopup, "gpt-4", "default")
	seedLog(t, u.Id, LogTypeTopup, "claude-3", "default")

	logs, _, err := GetUserLogs(u.Id, LogTypeUnknown, 0, 0, "gpt-4", "", 0, 100, "")
	if err != nil {
		t.Fatalf("GetUserLogs by model: %v", err)
	}
	for _, log := range logs {
		if !strings.Contains(log.ModelName, "gpt-4") {
			t.Errorf("expected gpt-4 only, got model=%q", log.ModelName)
		}
	}
}

// ---------------------------------------------------------------------------
// Option — UpdateOption / AllOption / InitOptionMap
// ---------------------------------------------------------------------------

func TestOption_UpdateAndGetAll(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	if err := UpdateOption("TestKey", "TestValue"); err != nil {
		t.Fatalf("UpdateOption: %v", err)
	}

	options, err := AllOption()
	if err != nil {
		t.Fatalf("AllOption: %v", err)
	}
	found := false
	for _, o := range options {
		if o.Key == "TestKey" && o.Value == "TestValue" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected TestKey=TestValue in options")
	}
}

func TestOption_UpdateOption_Overwrite(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	if err := UpdateOption("OverwriteKey", "first"); err != nil {
		t.Fatalf("UpdateOption first: %v", err)
	}
	if err := UpdateOption("OverwriteKey", "second"); err != nil {
		t.Fatalf("UpdateOption second: %v", err)
	}

	options, err := AllOption()
	if err != nil {
		t.Fatalf("AllOption: %v", err)
	}
	count := 0
	for _, o := range options {
		if o.Key == "OverwriteKey" {
			count++
			if o.Value != "second" {
				t.Errorf("expected value=second, got %q", o.Value)
			}
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 entry for OverwriteKey, got %d", count)
	}
}

func TestOption_InitOptionMap(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	// Should not panic; populates OptionMap
	InitOptionMap()

	common.OptionMapRWMutex.RLock()
	_, hasKey := common.OptionMap["LogConsumeEnabled"]
	common.OptionMapRWMutex.RUnlock()

	if !hasKey {
		t.Error("expected LogConsumeEnabled in OptionMap after InitOptionMap")
	}
}

// ---------------------------------------------------------------------------
// User misc — GetUsernameById, GetRootUser
// ---------------------------------------------------------------------------

func TestUser_GetUsernameById_Extra4(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "named_user2", "named2@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")

	name, err := GetUsernameById(u.Id, false)
	if err != nil {
		t.Fatalf("GetUsernameById: %v", err)
	}
	if name != "named_user2" {
		t.Errorf("expected named_user2, got %q", name)
	}
}
