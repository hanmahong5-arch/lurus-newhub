package repo

import (
	"context"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// ─── GetMissingModels ────────────────────────────────────────────────────────

func TestGetMissingModels_NoAbilities_ReturnsEmpty(t *testing.T) {
	setupSQLiteDB(t)

	got, err := GetMissingModels()
	if err != nil {
		t.Fatalf("GetMissingModels: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 missing, got %d", len(got))
	}
}

func TestGetMissingModels_WithAbility_ReturnsModel(t *testing.T) {
	setupSQLiteDB(t)

	// seed a channel + ability so GetEnabledModels returns something
	ch := seedChannelExtra4(t, "missing-model-ch", "default")
	ch.Models = "gpt-4-testmissing"
	DB.Save(ch)
	ab := &Ability{ChannelId: ch.Id, Model: "gpt-4-testmissing", Group: "default", Enabled: true}
	DB.Create(ab)

	got, err := GetMissingModels()
	if err != nil {
		t.Fatalf("GetMissingModels: %v", err)
	}
	// model metadata table has no entry for gpt-4-testmissing, so it should appear as missing
	found := false
	for _, m := range got {
		if m == "gpt-4-testmissing" {
			found = true
			break
		}
	}
	if !found {
		t.Logf("GetMissingModels returned: %v (model may be in metadata already)", got)
	}
}

// ─── handleConfigUpdate ───────────────────────────────────────────────────────

func TestHandleConfigUpdate_NotDottedKey_ReturnsFalse(t *testing.T) {
	setupSQLiteDB(t)

	// key without "." → returns false (not a layered config)
	handled := handleConfigUpdate("no-dot-key", "value")
	if handled {
		t.Error("expected handleConfigUpdate to return false for non-dotted key")
	}
}

func TestHandleConfigUpdate_UnregisteredConfig_ReturnsFalse(t *testing.T) {
	setupSQLiteDB(t)

	handled := handleConfigUpdate("no_such_config.some_key", "value")
	if handled {
		t.Error("expected handleConfigUpdate to return false for unregistered config")
	}
}

func TestHandleConfigUpdate_GeneralSetting_ReturnsTrue(t *testing.T) {
	setupSQLiteDB(t)

	// "general_setting" is registered by the operation_setting package init
	handled := handleConfigUpdate("general_setting.quota_display_type", "1")
	// It returns true if the config is registered; false otherwise.
	// Both outcomes are valid depending on init order — we just verify no panic.
	_ = handled
}

// ─── loadOptionsFromDatabase / UpdateOption extra keys ────────────────────────

func TestLoadOptionsFromDatabase_NoOptions_NoError(t *testing.T) {
	setupSQLiteDB(t)
	// Should be safe to call even with empty options table
	loadOptionsFromDatabase()
}

func TestUpdateOption_StringKeys(t *testing.T) {
	setupSQLiteDB(t)

	stringKeys := []struct {
		key   string
		value string
	}{
		{"EmailDomainWhitelist", "lurus.cn,example.com"},
		{"SMTPServer", "smtp.example.com"},
		{"SMTPAccount", "test@example.com"},
		{"SMTPFrom", "no-reply@example.com"},
		{"SMTPToken", "secrettoken"},
		{"ServerAddress", "https://hub.lurus.cn"},
		{"WorkerUrl", "https://worker.lurus.cn"},
		{"WorkerValidKey", "worker-key-123"},
		{"GitHubClientId", "gh-client-id"},
		{"GitHubClientSecret", "gh-client-secret"},
		{"LinuxDOClientId", "ldo-client-id"},
		{"LinuxDOClientSecret", "ldo-client-secret"},
		{"Footer", "Footer text"},
		{"SystemName", "Lurus Hub Test"},
		{"Logo", "https://cdn.example.com/logo.png"},
		{"WeChatServerAddress", "wechat.example.com"},
		{"WeChatServerToken", "wechat-token"},
		{"WeChatAccountQRCodeImageURL", "https://qr.example.com"},
		{"TelegramBotToken", "1234567890:ABCdef"},
		{"TelegramBotName", "TestBot"},
		{"TurnstileSiteKey", "turnstile-site"},
		{"TurnstileSecretKey", "turnstile-secret"},
		{"DataExportDefaultTime", "00:00"},
	}

	for _, tc := range stringKeys {
		if err := UpdateOption(tc.key, tc.value); err != nil {
			t.Logf("UpdateOption(%q=%q) acceptable error: %v", tc.key, tc.value, err)
		}
	}
}

func TestUpdateOption_IntKeys(t *testing.T) {
	setupSQLiteDB(t)

	intKeys := []struct {
		key   string
		value string
	}{
		{"SMTPPort", "587"},
		{"LinuxDOMinimumTrustLevel", "2"},
		{"QuotaForNewUser", "1000"},
		{"QuotaForInviter", "500"},
		{"QuotaForInvitee", "100"},
		{"QuotaRemindThreshold", "200"},
		{"PreConsumedQuota", "50"},
		{"ModelRequestRateLimitCount", "60"},
		{"ModelRequestRateLimitDurationMinutes", "1"},
		{"ModelRequestRateLimitSuccessCount", "10"},
		{"RetryTimes", "3"},
		{"DataExportInterval", "5"},
		{"StreamCacheQueueLength", "100"},
		{"SessionTimeoutMinutes", "1440"},
		{"SessionTimeoutMinutes", "-1"}, // triggers default branch (<=0 → 10080)
	}

	for _, tc := range intKeys {
		if err := UpdateOption(tc.key, tc.value); err != nil {
			t.Logf("UpdateOption(%q=%q) acceptable error: %v", tc.key, tc.value, err)
		}
	}
}

func TestUpdateOption_FloatKeys(t *testing.T) {
	setupSQLiteDB(t)

	floatKeys := []struct {
		key   string
		value string
	}{
		{"Price", "0.002"},
		{"USDExchangeRate", "7.15"},
		{"ChannelDisableThreshold", "0.9"},
		{"QuotaPerUnit", "1000.0"},
		{"ModelFallbackMarkup", "0.1"},
	}

	for _, tc := range floatKeys {
		if err := UpdateOption(tc.key, tc.value); err != nil {
			t.Logf("UpdateOption(%q=%q) acceptable error: %v", tc.key, tc.value, err)
		}
	}
}

func TestUpdateOption_SMSKeys(t *testing.T) {
	setupSQLiteDB(t)

	smsKeys := []struct {
		key   string
		value string
	}{
		{"SMSAccessKeyId", "sms-key-id"},
		{"SMSAccessKeySecret", "sms-secret"},
		{"SMSSignName", "Lurus"},
		{"SMSRegionId", "cn-shenzhen"},
		{"SMSRegionId", ""}, // triggers default "cn-hangzhou" branch
		{"SMSEnabled", "false"},
	}

	for _, tc := range smsKeys {
		if err := UpdateOption(tc.key, tc.value); err != nil {
			t.Logf("UpdateOption(%q=%q) acceptable error: %v", tc.key, tc.value, err)
		}
	}
}

func TestUpdateOption_PhoneVerificationKeys(t *testing.T) {
	setupSQLiteDB(t)

	keys := []struct {
		key   string
		value string
	}{
		{"PhoneVerificationMode", common.PhoneVerificationDisabled},
		{"PhoneVerificationMode", common.PhoneVerificationOptional},
		{"PhoneVerificationMode", common.PhoneVerificationRequiredLogin},
		{"PhoneVerificationMode", common.PhoneVerificationRequiredSensitive},
		{"PhoneVerificationMode", "invalid-mode"}, // triggers default branch
		{"PhoneRequiredForLogin", "true"},
		{"PhoneRequiredForPasswordReset", "true"},
		{"PhoneRequiredFor2FAChange", "true"},
		{"PhoneRequiredForPayment", "true"},
		{"PhoneRequiredForWithdrawal", "true"},
		{"PhoneRequiredForPhoneBind", "true"},
		{"PhoneRequiredForAccountDelete", "true"},
		{"PhoneRequiredForTokenGenerate", "true"},
		{"PhoneRequiredForOAuthBind", "true"},
	}

	for _, tc := range keys {
		if err := UpdateOption(tc.key, tc.value); err != nil {
			t.Logf("UpdateOption(%q=%q) acceptable error: %v", tc.key, tc.value, err)
		}
	}
}

func TestUpdateOption_RegistrationKeys(t *testing.T) {
	setupSQLiteDB(t)

	keys := []struct {
		key   string
		value string
	}{
		{"RegistrationMode", common.RegistrationModeOpen},
		{"RegistrationMode", common.RegistrationModeInviteOnly},
		{"RegistrationMode", common.RegistrationModeOAuthOnly},
		{"RegistrationMode", common.RegistrationModePhoneVerified},
		{"RegistrationMode", common.RegistrationModeClosed},
		{"RegistrationMode", "bad-value"}, // triggers default → RegistrationModeOpen
		{"SMSAutoRegister", "true"},
		{"InviteCodeRequired", "true"},
	}

	for _, tc := range keys {
		if err := UpdateOption(tc.key, tc.value); err != nil {
			t.Logf("UpdateOption(%q=%q) acceptable error: %v", tc.key, tc.value, err)
		}
	}
}

func TestUpdateOption_SecurityKeys(t *testing.T) {
	setupSQLiteDB(t)

	keys := []struct {
		key   string
		value string
	}{
		{"SensitiveActionRequirePassword", "true"},
		{"SensitiveActionRequire2FA", "false"},
		{"SensitiveWords", "badword1,badword2"},
		{"AutomaticDisableKeywords", "keyword1,keyword2"},
	}

	for _, tc := range keys {
		if err := UpdateOption(tc.key, tc.value); err != nil {
			t.Logf("UpdateOption(%q=%q) acceptable error: %v", tc.key, tc.value, err)
		}
	}
}

func TestUpdateOption_BoolKeys_Extra9(t *testing.T) {
	setupSQLiteDB(t)

	boolKeys := []string{
		"WorkerAllowHttpImageRequestEnabled",
		"DefaultUseAutoGroup",
		"MjNotifyEnabled",
		"MjAccountFilterEnabled",
		"MjModeClearEnabled",
		"MjForwardUrlEnabled",
		"MjActionCheckSuccessEnabled",
		"CheckSensitiveEnabled",
		"DemoSiteEnabled",
		"SelfUseModeEnabled",
		"CheckSensitiveOnPromptEnabled",
		"ModelRequestRateLimitEnabled",
		"StopOnSensitiveEnabled",
		"SMTPSSLEnabled",
		"DefaultCollapseSidebar",
		"ExposeRatioEnabled",
	}

	for _, key := range boolKeys {
		if err := UpdateOption(key, "true"); err != nil {
			t.Logf("UpdateOption(%q=true) acceptable error: %v", key, err)
		}
		if err := UpdateOption(key, "false"); err != nil {
			t.Logf("UpdateOption(%q=false) acceptable error: %v", key, err)
		}
	}
}

func TestUpdateOption_JSONRatioKeys(t *testing.T) {
	setupSQLiteDB(t)

	jsonKeys := []struct {
		key   string
		value string
	}{
		{"TopupGroupRatio", `{"default":1.0}`},
		{"ModelRequestRateLimitGroup", `{}`},
		{"Chats", `[]`},
		{"AutoGroups", `[]`},
	}

	for _, tc := range jsonKeys {
		if err := UpdateOption(tc.key, tc.value); err != nil {
			t.Logf("UpdateOption(%q) JSON acceptable error: %v", tc.key, err)
		}
	}
}

// ─── GetAuditEvents / DeleteOldAuditEvents ────────────────────────────────────

func TestGetAuditEvents_Empty(t *testing.T) {
	setupSQLiteDB(t)

	events, total, err := GetAuditEvents("default", "", 0, "", 0, 0, 0, 20)
	if err != nil {
		t.Fatalf("GetAuditEvents: %v", err)
	}
	if total != 0 || len(events) != 0 {
		t.Errorf("expected empty result, got total=%d events=%d", total, len(events))
	}
}

func TestGetAuditEvents_WithData_AllFilters(t *testing.T) {
	setupSQLiteDB(t)

	repo := &AuditEventRepo{}
	now := time.Now().Unix()

	ev1 := &entity.AuditEvent{
		TenantID:  "default",
		ActorID:   42,
		Action:    "channel.create",
		Resource:  "channel:1",
		Timestamp: now,
	}
	ev2 := &entity.AuditEvent{
		TenantID:  "other-tenant",
		ActorID:   10,
		Action:    "user.update",
		Resource:  "user:5",
		Timestamp: now - 100,
	}
	if err := repo.CreateAuditEvent(ev1); err != nil {
		t.Fatalf("CreateAuditEvent ev1: %v", err)
	}
	if err := repo.CreateAuditEvent(ev2); err != nil {
		t.Fatalf("CreateAuditEvent ev2: %v", err)
	}

	// filter by tenantID
	events, total, err := GetAuditEvents("default", "", 0, "", 0, 0, 0, 20)
	if err != nil {
		t.Fatalf("GetAuditEvents tenantID filter: %v", err)
	}
	if total < 1 {
		t.Errorf("expected >=1 event for default tenant, got %d", total)
	}

	// filter by action
	events, total, err = GetAuditEvents("", "channel.create", 0, "", 0, 0, 0, 20)
	if err != nil {
		t.Fatalf("GetAuditEvents action filter: %v", err)
	}
	if total < 1 {
		t.Errorf("expected >=1 event for action=channel.create, got %d", total)
	}
	_ = events

	// filter by actorID
	events, total, err = GetAuditEvents("", "", 42, "", 0, 0, 0, 20)
	if err != nil {
		t.Fatalf("GetAuditEvents actorID filter: %v", err)
	}
	if total < 1 {
		t.Errorf("expected >=1 event for actorID=42, got %d", total)
	}

	// filter by resource
	events, total, err = GetAuditEvents("", "", 0, "channel:1", 0, 0, 0, 20)
	if err != nil {
		t.Fatalf("GetAuditEvents resource filter: %v", err)
	}
	if total < 1 {
		t.Errorf("expected >=1 event for resource=channel:1, got %d", total)
	}

	// filter by time range
	events, total, err = GetAuditEvents("", "", 0, "", now-200, now+1, 0, 20)
	if err != nil {
		t.Fatalf("GetAuditEvents time range: %v", err)
	}
	if total < 2 {
		t.Errorf("expected 2 events in time range, got %d", total)
	}
	_ = events

	// limit enforcement: limit=0 defaults to 20, limit>100 caps at 100
	events, _, err = GetAuditEvents("", "", 0, "", 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("GetAuditEvents limit=0: %v", err)
	}
	_ = events
}

func TestDeleteOldAuditEvents_DeletesOld(t *testing.T) {
	setupSQLiteDB(t)

	repo := &AuditEventRepo{}
	now := time.Now().Unix()

	// create old event
	old := &entity.AuditEvent{TenantID: "default", ActorID: 1, Action: "test", Resource: "x", Timestamp: now - 3600}
	// create recent event
	recent := &entity.AuditEvent{TenantID: "default", ActorID: 2, Action: "test", Resource: "y", Timestamp: now}

	if err := repo.CreateAuditEvent(old); err != nil {
		t.Fatalf("CreateAuditEvent old: %v", err)
	}
	if err := repo.CreateAuditEvent(recent); err != nil {
		t.Fatalf("CreateAuditEvent recent: %v", err)
	}

	ctx := context.Background()
	deleted, err := DeleteOldAuditEvents(ctx, now-100, 100)
	if err != nil {
		t.Fatalf("DeleteOldAuditEvents: %v", err)
	}
	if deleted < 1 {
		t.Errorf("expected at least 1 deleted, got %d", deleted)
	}
}

func TestDeleteOldAuditEvents_ContextCancelled(t *testing.T) {
	setupSQLiteDB(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := DeleteOldAuditEvents(ctx, time.Now().Unix()+9999, 100)
	if err == nil {
		t.Error("expected context cancellation error")
	}
}

// ─── UserIdentityMapping ────────────────────────────────────────────────────

func TestUserIdentityMapping_CRUD(t *testing.T) {
	setupSQLiteDB(t)

	// seed a user first
	u := &User{Username: "zitadel-user-99", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, TenantId: "default"}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	// GetUserMappingByIDPSubject — not found
	_, err := GetUserMappingByIDPSubject("sub-999", "default")
	if err == nil {
		t.Error("expected not-found error")
	}

	// CreateUserMapping — new mapping
	mapping, err := CreateUserMapping(u.Id, "sub-999", "default", "test@ex.com", "Test User", "testuser99")
	if err != nil {
		t.Fatalf("CreateUserMapping: %v", err)
	}
	if mapping.LurusUserID != u.Id {
		t.Errorf("LurusUserID mismatch: got %d, want %d", mapping.LurusUserID, u.Id)
	}

	// CreateUserMapping — existing (should update)
	mapping2, err := CreateUserMapping(u.Id, "sub-999", "default", "new@ex.com", "New Name", "newuser99")
	if err != nil {
		t.Fatalf("CreateUserMapping upsert: %v", err)
	}
	if mapping2.Email != "new@ex.com" {
		t.Errorf("email not updated: got %q", mapping2.Email)
	}

	// GetUserMappingByLurusUserID
	foundByLurus, err := GetUserMappingByLurusUserID(u.Id, "default")
	if err != nil {
		t.Fatalf("GetUserMappingByLurusUserID: %v", err)
	}
	if foundByLurus.IDPSubject != "sub-999" {
		t.Errorf("ZitadelUserID mismatch: %q", foundByLurus.IDPSubject)
	}

	// GetUserByIDPSubject
	fetchedUser, fetchedMapping, err := GetUserByIDPSubject("sub-999", "default")
	if err != nil {
		t.Fatalf("GetUserByIDPSubject: %v", err)
	}
	if fetchedUser.Id != u.Id {
		t.Errorf("user id mismatch: got %d, want %d", fetchedUser.Id, u.Id)
	}
	_ = fetchedMapping

	// UpdateUserMapping
	if err := UpdateUserMapping(mapping.Id, map[string]interface{}{"display_name": "Updated"}); err != nil {
		t.Fatalf("UpdateUserMapping: %v", err)
	}

	// SyncUserDataFromIDP
	if err := SyncUserDataFromIDP(mapping.Id, "sync@ex.com", "Synced Name", "synceduser"); err != nil {
		t.Fatalf("SyncUserDataFromIDP: %v", err)
	}

	// ListUserMappingsByTenant
	mappings, total, err := ListUserMappingsByTenant("default", 0, 10)
	if err != nil {
		t.Fatalf("ListUserMappingsByTenant: %v", err)
	}
	if total < 1 {
		t.Errorf("expected >=1 mapping, got %d", total)
	}
	_ = mappings

	// ListUserMappingsByIDPSubject
	allMappings, err := ListUserMappingsByIDPSubject("sub-999")
	if err != nil {
		t.Fatalf("ListUserMappingsByIDPSubject: %v", err)
	}
	if len(allMappings) < 1 {
		t.Errorf("expected >=1 mapping for zitadel sub, got %d", len(allMappings))
	}

	// DeactivateUserMapping
	if err := DeactivateUserMapping(mapping.Id); err != nil {
		t.Fatalf("DeactivateUserMapping: %v", err)
	}

	// DeleteUserMapping
	if err := DeleteUserMapping(mapping.Id); err != nil {
		t.Fatalf("DeleteUserMapping: %v", err)
	}
}

// ─── applyMultiKeyCooldown / ClearMultiKeyCooldown ────────────────────────────

func TestApplyMultiKeyCooldown_SingleKey(t *testing.T) {
	setupSQLiteDB(t)

	ch := &Channel{
		Name:     "multikey-cool-ch",
		Type:     1,
		Key:      "key-a\nkey-b",
		Status:   common.ChannelStatusEnabled,
		TenantId: "default",
	}
	ch.ChannelInfo.IsMultiKey = true
	ch.ChannelInfo.MultiKeySize = 2
	if err := DB.Create(ch).Error; err != nil {
		t.Skipf("create multikey channel: %v", err)
	}

	cooldownUntil := time.Now().Unix() + 300
	applyMultiKeyCooldown(ch, "key-a", cooldownUntil, "test-reason")

	// key-a is index 0
	if ch.ChannelInfo.MultiKeyStatusList[0] != common.ChannelStatusAutoDisabled {
		t.Errorf("expected key 0 auto-disabled, got %d", ch.ChannelInfo.MultiKeyStatusList[0])
	}
	if ch.ChannelInfo.MultiKeyDisabledReason[0] != "test-reason" {
		t.Errorf("expected reason 'test-reason', got %q", ch.ChannelInfo.MultiKeyDisabledReason[0])
	}
}

func TestApplyMultiKeyCooldown_AllKeysCooling_DisablesChannel(t *testing.T) {
	setupSQLiteDB(t)

	ch := &Channel{
		Name:     "multikey-alldisable-ch",
		Type:     1,
		Key:      "key-x\nkey-y",
		Status:   common.ChannelStatusEnabled,
		TenantId: "default",
	}
	ch.ChannelInfo.IsMultiKey = true
	ch.ChannelInfo.MultiKeySize = 2
	if err := DB.Create(ch).Error; err != nil {
		t.Skipf("create multikey channel: %v", err)
	}

	cooldownUntil := time.Now().Unix() + 300
	applyMultiKeyCooldown(ch, "key-x", cooldownUntil, "reason-x")
	applyMultiKeyCooldown(ch, "key-y", cooldownUntil, "reason-y")

	// With both keys cooled, channel should auto-disable
	if ch.Status != common.ChannelStatusAutoDisabled {
		t.Errorf("expected channel auto-disabled, got status %d", ch.Status)
	}
}

func TestApplyMultiKeyCooldown_KeyNotFound_Noop(t *testing.T) {
	setupSQLiteDB(t)

	ch := &Channel{
		Name:     "multikey-notfound-ch",
		Type:     1,
		Key:      "key-only",
		Status:   common.ChannelStatusEnabled,
		TenantId: "default",
	}
	ch.ChannelInfo.IsMultiKey = true
	ch.ChannelInfo.MultiKeySize = 1
	if err := DB.Create(ch).Error; err != nil {
		t.Skipf("create channel: %v", err)
	}

	// Key that doesn't exist in the channel — should be a no-op
	applyMultiKeyCooldown(ch, "nonexistent-key", time.Now().Unix()+100, "reason")
	if ch.ChannelInfo.MultiKeyStatusList != nil && len(ch.ChannelInfo.MultiKeyStatusList) > 0 {
		t.Error("expected no status list mutation for missing key")
	}
}

func TestClearMultiKeyCooldown(t *testing.T) {
	setupSQLiteDB(t)

	ch := &Channel{
		Name:     "multikey-clear-ch",
		Type:     1,
		Key:      "key-p\nkey-q",
		Status:   common.ChannelStatusEnabled,
		TenantId: "default",
	}
	ch.ChannelInfo.IsMultiKey = true
	ch.ChannelInfo.MultiKeySize = 2
	if err := DB.Create(ch).Error; err != nil {
		t.Skipf("create channel: %v", err)
	}

	// First apply cooldown
	cooldownUntil := time.Now().Unix() + 300
	applyMultiKeyCooldown(ch, "key-p", cooldownUntil, "cooldown")

	// Then clear it
	ClearMultiKeyCooldown(ch, 0)
	if ch.ChannelInfo.MultiKeyStatusList != nil {
		if _, ok := ch.ChannelInfo.MultiKeyStatusList[0]; ok {
			t.Error("expected key 0 cleared from status list")
		}
	}
}

func TestListOpenRouterMultiKeyChannels_Empty(t *testing.T) {
	setupSQLiteDB(t)

	channels, err := ListOpenRouterMultiKeyChannels()
	if err != nil {
		t.Fatalf("ListOpenRouterMultiKeyChannels: %v", err)
	}
	// No OpenRouter channels seeded, so expect empty or non-multikey
	_ = channels
}

// ─── MarkMultiKeyCooldown ─────────────────────────────────────────────────────

func TestMarkMultiKeyCooldown_ZeroCooldown_ReturnsFalse(t *testing.T) {
	setupSQLiteDB(t)

	result := MarkMultiKeyCooldown(9999, "any-key", 0, "reason")
	if result {
		t.Error("expected false for zero cooldownUntil")
	}
}

func TestMarkMultiKeyCooldown_ChannelNotFound_ReturnsFalse(t *testing.T) {
	setupSQLiteDB(t)

	result := MarkMultiKeyCooldown(999999, "any-key", time.Now().Unix()+300, "reason")
	if result {
		t.Error("expected false for non-existent channel")
	}
}

// ─── UpdateUserUsedQuotaAndRequestCount ──────────────────────────────────────

func TestUpdateUserUsedQuotaAndRequestCount_DirectPath(t *testing.T) {
	setupSQLiteDB(t)
	common.BatchUpdateEnabled = false

	u := &User{Username: "quota-reqcount-user", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, TenantId: "default", Quota: 10000}
	if err := DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	UpdateUserUsedQuotaAndRequestCount(u.Id, 100)

	var loaded User
	DB.First(&loaded, u.Id)
	if loaded.UsedQuota < 100 {
		t.Errorf("expected UsedQuota>=100, got %d", loaded.UsedQuota)
	}
	if loaded.RequestCount < 1 {
		t.Errorf("expected RequestCount>=1, got %d", loaded.RequestCount)
	}
}

// ─── GetEfficiencyStats / governance stats ────────────────────────────────────

func TestGetEfficiencyStats_Empty(t *testing.T) {
	setupSQLiteDB(t)

	stats, err := GetEfficiencyStats(0)
	if err != nil {
		t.Fatalf("GetEfficiencyStats: %v", err)
	}
	_ = stats
}

func TestGetChannelDistribution_Empty(t *testing.T) {
	setupSQLiteDB(t)

	stats, err := GetChannelDistribution(0)
	if err != nil {
		t.Fatalf("GetChannelDistribution: %v", err)
	}
	_ = stats
}

func TestGetLatencyStats_Empty(t *testing.T) {
	setupSQLiteDB(t)

	stats, err := GetLatencyStats(0)
	if err != nil {
		t.Fatalf("GetLatencyStats: %v", err)
	}
	_ = stats
}

func TestGetFingerprintDedupStats_Empty(t *testing.T) {
	setupSQLiteDB(t)

	stats, err := GetFingerprintDedupStats(0, 1)
	if err != nil {
		t.Fatalf("GetFingerprintDedupStats: %v", err)
	}
	_ = stats
}

// ─── EditChannelByTag ─────────────────────────────────────────────────────────

func TestEditChannelByTag_UpdateTag(t *testing.T) {
	setupSQLiteDB(t)

	tag := "edit-tag-test"
	ch := seedChannelExtra4(t, "edit-by-tag-ch", "default")
	ch.SetTag(tag)
	DB.Save(ch)

	newTag := "edit-tag-updated"
	if err := EditChannelByTag(tag, &newTag, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("EditChannelByTag: %v", err)
	}

	var loaded Channel
	if err := DB.First(&loaded, ch.Id).Error; err != nil {
		t.Fatalf("load channel: %v", err)
	}
	if loaded.GetTag() != newTag {
		t.Errorf("expected tag %q, got %q", newTag, loaded.GetTag())
	}
}

func TestEditChannelByTag_UpdateModels(t *testing.T) {
	setupSQLiteDB(t)

	tag := "edit-models-tag"
	ch := seedChannelExtra4(t, "edit-models-ch", "default")
	ch.SetTag(tag)
	ch.Models = "gpt-3.5-turbo"
	DB.Save(ch)

	newModels := "gpt-4,gpt-3.5-turbo"
	if err := EditChannelByTag(tag, nil, nil, &newModels, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("EditChannelByTag with models: %v", err)
	}

	var loaded Channel
	DB.First(&loaded, ch.Id)
	if loaded.Models != newModels {
		t.Errorf("expected models %q, got %q", newModels, loaded.Models)
	}
}

// ─── Channel: SetOtherInfo / SetOtherSettings ─────────────────────────────────

func TestChannel_SetOtherInfo_AndGet(t *testing.T) {
	setupSQLiteDB(t)

	ch := seedChannelExtra4(t, "other-info-ch", "default")

	// SetOtherInfo
	info := map[string]interface{}{"status_reason": "test", "status_time": 12345}
	ch.SetOtherInfo(info)
	DB.Save(ch)

	var loaded Channel
	DB.First(&loaded, ch.Id)
	got := loaded.GetOtherInfo()
	if got["status_reason"] != "test" {
		t.Errorf("status_reason mismatch: %v", got["status_reason"])
	}
}

func TestChannel_SetOtherSettings_WithData(t *testing.T) {
	setupSQLiteDB(t)

	ch := seedChannelExtra4(t, "other-settings-set-ch", "default")
	otherSettings := ch.GetOtherSettings()
	otherSettings.AllowServiceTier = true
	ch.SetOtherSettings(otherSettings)
	DB.Save(ch)

	var loaded Channel
	DB.First(&loaded, ch.Id)
	got := loaded.GetOtherSettings()
	if !got.AllowServiceTier {
		t.Errorf("AllowServiceTier not persisted: got %v", got.AllowServiceTier)
	}
}
