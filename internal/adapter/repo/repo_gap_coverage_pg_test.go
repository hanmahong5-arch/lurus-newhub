package repo

// repo_gap_coverage_pg_test.go — targeted coverage for the SPECIFIC uncovered
// branches left by the existing repo suite. Every test seeds real rows on the
// disposable Postgres (SetupTestDB), exercises the branch, then re-reads and
// asserts the concrete state delta. Business-acceptance focus: multi-tenant +
// billing isolation at the repo layer (key rotation never returns a
// cooled/disabled key; per-user quota mutators touch only the target row;
// mapping lookups never leak across platform accounts).

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// ─── channel.go GetNextEnabledKey — polling / random / all-cooling branches ───

func TestGetNextEnabledKey_PollingSkipsDisabled_PG(t *testing.T) {
	SetupTestDB(t)
	ch := &Channel{
		Id:       8001,
		Type:     1,
		Status:   common.ChannelStatusEnabled,
		Name:     "poll",
		Key:      "pk0\npk1\npk2",
		Models:   "m",
		Group:    "default",
		TenantId: "default",
		ChannelInfo: entity.ChannelInfo{
			IsMultiKey:           true,
			MultiKeySize:         3,
			MultiKeyMode:         constant.MultiKeyModePolling,
			MultiKeyStatusList:   map[int]int{0: common.ChannelStatusManuallyDisabled},
			MultiKeyPollingIndex: 0,
		},
	}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	// Polling starts at index 0, which is disabled → must skip to pk1.
	key, idx, apiErr := ch.GetNextEnabledKey()
	if apiErr != nil {
		t.Fatalf("GetNextEnabledKey polling: %v", apiErr)
	}
	if key != "pk1" || idx != 1 {
		t.Fatalf("polling must skip disabled key 0 and pick pk1/idx1, got %q/%d", key, idx)
	}

	// The next-position polling index (idx+1 = 2) must be persisted for round-robin.
	reloaded, err := GetChannelById(8001, true)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if reloaded.ChannelInfo.MultiKeyPollingIndex != 2 {
		t.Fatalf("polling index must advance to 2, got %d", reloaded.ChannelInfo.MultiKeyPollingIndex)
	}
}

func TestGetNextEnabledKey_RandomNeverPicksDisabled_PG(t *testing.T) {
	SetupTestDB(t)
	ch := &Channel{
		Id:       8002,
		Type:     1,
		Status:   common.ChannelStatusEnabled,
		Name:     "rand",
		Key:      "rk0\nrk1\nrk2",
		Models:   "m",
		Group:    "default",
		TenantId: "default",
		ChannelInfo: entity.ChannelInfo{
			IsMultiKey:         true,
			MultiKeySize:       3,
			MultiKeyMode:       constant.MultiKeyModeRandom,
			MultiKeyStatusList: map[int]int{2: common.ChannelStatusAutoDisabled},
		},
	}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	// Randomised selection over many calls must never return the disabled key 2.
	for i := 0; i < 50; i++ {
		key, idx, apiErr := ch.GetNextEnabledKey()
		if apiErr != nil {
			t.Fatalf("GetNextEnabledKey random: %v", apiErr)
		}
		if idx == 2 || key == "rk2" {
			t.Fatalf("random selection returned disabled key (idx=%d key=%q)", idx, key)
		}
		if idx != 0 && idx != 1 {
			t.Fatalf("random selection out of enabled set {0,1}, got %d", idx)
		}
	}
}

func TestGetNextEnabledKey_AllCoolingReturnsRetryAfter(t *testing.T) {
	// Pure struct path (errors before any DB access) — no harness needed.
	const c0, c1 = int64(2_000_000_000), int64(1_999_999_000)
	ch := &Channel{
		Key: "ck0\nck1",
		ChannelInfo: entity.ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: 2,
			MultiKeyStatusList: map[int]int{
				0: common.ChannelStatusAutoDisabled,
				1: common.ChannelStatusAutoDisabled,
			},
			MultiKeyCooldownUntil: map[int]int64{0: c0, 1: c1},
		},
	}
	_, _, apiErr := ch.GetNextEnabledKey()
	if apiErr == nil {
		t.Fatal("all-keys-cooling must return an error, got nil")
	}
	// The earliest recovery deadline (c1) must be surfaced for downstream Retry-After.
	if apiErr.RetryAfterUnix != c1 {
		t.Fatalf("RetryAfterUnix must be the earliest cooldown %d, got %d", c1, apiErr.RetryAfterUnix)
	}
}

// ─── channel.go GetSetting / GetOtherSettings — malformed-JSON self-heal ──────

func TestGetSetting_SelfHealsCorruptJSON_PG(t *testing.T) {
	SetupTestDB(t)
	bad := "{not-valid-json"
	ch := &Channel{
		Id:       8010,
		Type:     1,
		Status:   common.ChannelStatusEnabled,
		Name:     "corrupt-setting",
		Key:      "k",
		Models:   "m",
		Group:    "default",
		TenantId: "default",
		Setting:  &bad,
	}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	// GetSetting on corrupt JSON returns the zero-value AND self-heals by nulling
	// the column so subsequent reads don't keep erroring.
	_ = ch.GetSetting()

	reloaded, err := GetChannelById(8010, true)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if reloaded.Setting != nil && *reloaded.Setting != "" {
		t.Fatalf("corrupt Setting must be self-healed to null, got %q", *reloaded.Setting)
	}
}

func TestGetOtherSettings_SelfHealsCorruptJSON_PG(t *testing.T) {
	SetupTestDB(t)
	ch := &Channel{
		Id:            8011,
		Type:          1,
		Status:        common.ChannelStatusEnabled,
		Name:          "corrupt-other",
		Key:           "k",
		Models:        "m",
		Group:         "default",
		TenantId:      "default",
		OtherSettings: "{broken",
	}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	_ = ch.GetOtherSettings()

	reloaded, err := GetChannelById(8011, true)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if reloaded.OtherSettings != "{}" {
		t.Fatalf("corrupt OtherSettings must self-heal to '{}', got %q", reloaded.OtherSettings)
	}
}

func TestGetSetting_ValidJSONDecodes_PG(t *testing.T) {
	SetupTestDB(t)
	valid := `{"force_format":true}`
	ch := &Channel{
		Id:       8012,
		Type:     1,
		Status:   common.ChannelStatusEnabled,
		Name:     "valid-setting",
		Key:      "k",
		Models:   "m",
		Group:    "default",
		TenantId: "default",
		Setting:  &valid,
	}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	// Valid JSON must decode without wiping the column.
	_ = ch.GetSetting()
	reloaded, err := GetChannelById(8012, true)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if reloaded.Setting == nil || *reloaded.Setting != valid {
		t.Fatalf("valid Setting must be preserved, got %v", reloaded.Setting)
	}
}

// ─── channel.go UpdateChannelUsedQuota — batch-enabled enqueue+flush ──────────

func TestUpdateChannelUsedQuota_BatchPath_PG(t *testing.T) {
	SetupTestDB(t)
	ch := &Channel{Id: 8020, Type: 1, Status: common.ChannelStatusEnabled, Name: "cq", Models: "m", Group: "default", TenantId: "default"}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	prev := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = true
	defer func() { common.BatchUpdateEnabled = prev }()

	// Batch path enqueues; flush drains it into the row.
	UpdateChannelUsedQuota(8020, 777)
	batchUpdate()

	reloaded, err := GetChannelById(8020, true)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if reloaded.UsedQuota != 777 {
		t.Fatalf("batch used_quota must be 777, got %d", reloaded.UsedQuota)
	}
}

// ─── channel_pool.go MarkMultiKeyCooldown — all-keys-cooling auto-disable ─────

func TestMarkMultiKeyCooldown_AutoDisablesWhenAllCooling_PG(t *testing.T) {
	SetupTestDB(t)
	ch := &Channel{
		Id:       8030,
		Type:     constant.ChannelTypeOpenRouter,
		Status:   common.ChannelStatusEnabled,
		Name:     "cool",
		Key:      "ak0\nak1",
		Models:   "m",
		Group:    "default",
		TenantId: "default",
		ChannelInfo: entity.ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: 2,
			MultiKeyMode: constant.MultiKeyModePolling,
		},
	}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	deadline := common.GetTimestamp() + 300

	// Cooling one of two keys must NOT flip the channel status yet.
	if !MarkMultiKeyCooldown(8030, "ak0", deadline, "rate-limited") {
		t.Fatal("first MarkMultiKeyCooldown should succeed")
	}
	mid, err := GetChannelById(8030, true)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if mid.Status != common.ChannelStatusEnabled {
		t.Fatalf("channel must stay enabled after cooling 1/2 keys, got status %d", mid.Status)
	}
	if mid.ChannelInfo.MultiKeyCooldownUntil[0] != deadline {
		t.Fatalf("key 0 cooldown deadline not persisted: %v", mid.ChannelInfo.MultiKeyCooldownUntil)
	}

	// Cooling the second (last) key flips the whole channel auto-disabled.
	if !MarkMultiKeyCooldown(8030, "ak1", deadline, "rate-limited") {
		t.Fatal("second MarkMultiKeyCooldown should succeed")
	}
	end, err := GetChannelById(8030, true)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if end.Status != common.ChannelStatusAutoDisabled {
		t.Fatalf("channel must auto-disable once all keys cool, got status %d", end.Status)
	}
}

// ─── governance.go GetEfficiencyStats — real grouping + avg computation ───────

func TestGetEfficiencyStats_GroupsAndComputesAverages_PG(t *testing.T) {
	SetupTestDB(t)
	start := common.GetTimestamp() - 3600

	// gpt-4: 2 reqs, quota 100+50=150, tokens (40+60)+(20+30)=150
	seedConsumeLog(t, "gpt-4", 100, 40, 60, ``)
	seedConsumeLog(t, "gpt-4", 50, 20, 30, ``)
	// claude-3: 1 req, quota 300, tokens 80+120=200
	seedConsumeLog(t, "claude-3", 300, 80, 120, ``)
	// zero-quota row must be excluded by the quota > 0 filter.
	seedConsumeLog(t, "gpt-4", 0, 5, 5, ``)

	stats, err := GetEfficiencyStats(start)
	if err != nil {
		t.Fatalf("GetEfficiencyStats: %v", err)
	}

	byModel := map[string]GovernanceEfficiencyStat{}
	for _, s := range stats {
		byModel[s.ModelName] = s
	}
	g, ok := byModel["gpt-4"]
	if !ok {
		t.Fatalf("gpt-4 group missing: %+v", stats)
	}
	if g.TotalRequests != 2 || g.TotalQuota != 150 || g.TotalTokens != 150 {
		t.Fatalf("gpt-4 aggregate wrong: %+v", g)
	}
	if g.AvgQuotaPerReq != 75 || g.AvgTokensPerReq != 75 {
		t.Fatalf("gpt-4 averages wrong: quota/req=%v tokens/req=%v", g.AvgQuotaPerReq, g.AvgTokensPerReq)
	}
	if g.QuotaPerToken != 1 {
		t.Fatalf("gpt-4 quota-per-token wrong: %v", g.QuotaPerToken)
	}
	c := byModel["claude-3"]
	if c.TotalRequests != 1 || c.TotalQuota != 300 || c.TotalTokens != 200 {
		t.Fatalf("claude-3 aggregate wrong: %+v", c)
	}
	// Ordered by total_quota DESC → claude-3 (300) before gpt-4 (150).
	if len(stats) < 2 || stats[0].ModelName != "claude-3" {
		t.Fatalf("expected claude-3 first by spend, got %+v", stats)
	}
}

// ─── log.go GetLogByKey — LOG_SQL_DSN (separate-log-DB) branch ────────────────

func TestGetLogByKey_SeparateLogDBBranch_PG(t *testing.T) {
	SetupTestDB(t)
	// Force the LOG_SQL_DSN code path (token lookup on DB, logs on LOG_DB).
	t.Setenv("LOG_SQL_DSN", "postgres://placeholder/logs")

	_, normal, _ := SeedTestUsers(t)
	tok := &Token{UserId: normal.Id, TenantId: "default", Key: common.GetRandomString(30), Status: common.TokenStatusEnabled, Name: "lk", CreatedTime: common.GetTimestamp(), ExpiredTime: -1, Group: "default"}
	if err := DB.Create(tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	lg := &entity.Log{UserId: normal.Id, Username: "n", Type: LogTypeConsume, CreatedAt: common.GetTimestamp(), ModelName: "gpt-4", Quota: 5, TokenId: tok.Id, TenantId: "default"}
	if err := LOG_DB.Create(lg).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}

	logs, err := GetLogByKey("sk-"+tok.Key, normal.Id, "default")
	if err != nil {
		t.Fatalf("GetLogByKey (LOG_SQL_DSN branch): %v", err)
	}
	if len(logs) != 1 || logs[0].TokenId != tok.Id {
		t.Fatalf("expected 1 log for token %d, got %+v", tok.Id, logs)
	}

	// Miss case: a non-existent key returns the not-found error (token lookup fails).
	if _, err := GetLogByKey("sk-does-not-exist", normal.Id, "default"); err == nil {
		t.Fatal("GetLogByKey with unknown key must return an error in LOG_SQL_DSN branch")
	}
}

// ─── user.go GetAllUsers — paginated unscoped listing ─────────────────────────

func TestGetAllUsers_Pagination_PG(t *testing.T) {
	SetupTestDB(t)
	SeedTestUsers(t) // 3 users

	users, total, err := GetAllUsers(&common.PageInfo{Page: 1, PageSize: 2})
	if err != nil {
		t.Fatalf("GetAllUsers: %v", err)
	}
	if total != 3 {
		t.Fatalf("want total 3, got %d", total)
	}
	if len(users) != 2 {
		t.Fatalf("want page size 2, got %d", len(users))
	}
	// id DESC ordering.
	if users[0].Id < users[1].Id {
		t.Fatalf("expected id DESC ordering, got %d then %d", users[0].Id, users[1].Id)
	}
	// Second page carries the remaining row.
	page2, _, err := GetAllUsers(&common.PageInfo{Page: 2, PageSize: 2})
	if err != nil {
		t.Fatalf("GetAllUsers page2: %v", err)
	}
	if len(page2) != 1 {
		t.Fatalf("want 1 row on page 2, got %d", len(page2))
	}
}

// ─── user.go updateUserUsedQuotaAndRequestCount — per-user delta isolation ────

func TestUpdateUserUsedQuotaAndRequestCount_TargetsOnlyOneRow_PG(t *testing.T) {
	SetupTestDB(t)
	_, userA, userB := SeedTestUsers(t)

	prev := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = false
	defer func() { common.BatchUpdateEnabled = prev }()

	UpdateUserUsedQuotaAndRequestCount(userA.Id, 123)

	var a, b User
	if err := DB.First(&a, userA.Id).Error; err != nil {
		t.Fatalf("reload A: %v", err)
	}
	if err := DB.First(&b, userB.Id).Error; err != nil {
		t.Fatalf("reload B: %v", err)
	}
	if a.UsedQuota != 123 || a.RequestCount != 1 {
		t.Fatalf("target user delta wrong: used=%d req=%d", a.UsedQuota, a.RequestCount)
	}
	if b.UsedQuota != 0 || b.RequestCount != 0 {
		t.Fatalf("non-target user must be untouched: used=%d req=%d", b.UsedQuota, b.RequestCount)
	}
}

// ─── user.go IncreaseDailyUsed — exact delta, only the target user ────────────

func TestIncreaseDailyUsed_ExactDeltaTargetOnly_PG(t *testing.T) {
	SetupTestDB(t)
	_, userA, userB := SeedTestUsers(t)

	if err := IncreaseDailyUsed(userA.Id, 250); err != nil {
		t.Fatalf("IncreaseDailyUsed: %v", err)
	}
	if err := IncreaseDailyUsed(userA.Id, 50); err != nil {
		t.Fatalf("IncreaseDailyUsed 2: %v", err)
	}

	var a, b User
	DB.First(&a, userA.Id)
	DB.First(&b, userB.Id)
	if a.DailyUsed != 300 {
		t.Fatalf("target daily_used must be 300, got %d", a.DailyUsed)
	}
	if b.DailyUsed != 0 {
		t.Fatalf("non-target daily_used must stay 0, got %d", b.DailyUsed)
	}

	// Negative amounts are rejected without mutating state.
	if err := IncreaseDailyUsed(userA.Id, -1); err == nil {
		t.Fatal("negative amount must be rejected")
	}
	DB.First(&a, userA.Id)
	if a.DailyUsed != 300 {
		t.Fatalf("rejected call must not mutate daily_used, got %d", a.DailyUsed)
	}
}

// ─── user.go ResetDailyQuota — idempotent same-day no-op vs stale reset ───────

func TestResetDailyQuota_ResetsStaleOnly_PG(t *testing.T) {
	SetupTestDB(t)
	_, userA, _ := SeedTestUsers(t)

	// Seed a stale reset (yesterday) and non-zero daily usage.
	now := common.GetTimestamp()
	stale := now - 2*86400
	if err := DB.Model(&User{}).Where("id = ?", userA.Id).
		Updates(map[string]interface{}{"daily_used": 999, "last_daily_reset": stale}).Error; err != nil {
		t.Fatalf("seed stale state: %v", err)
	}

	// First reset: last_daily_reset is stale → clears daily_used.
	if err := ResetDailyQuota(userA.Id); err != nil {
		t.Fatalf("ResetDailyQuota: %v", err)
	}
	var a User
	DB.First(&a, userA.Id)
	if a.DailyUsed != 0 {
		t.Fatalf("stale reset must clear daily_used, got %d", a.DailyUsed)
	}

	// Bump usage again; a same-day second reset must be a no-op (RowsAffected 0).
	DB.Model(&User{}).Where("id = ?", userA.Id).Update("daily_used", 42)
	if err := ResetDailyQuota(userA.Id); err != nil {
		t.Fatalf("ResetDailyQuota same-day: %v", err)
	}
	DB.First(&a, userA.Id)
	if a.DailyUsed != 42 {
		t.Fatalf("same-day reset must be a no-op, daily_used got clobbered to %d", a.DailyUsed)
	}
}

// ─── user.go SwitchToFallbackGroup — eligibility gate + target isolation ──────

func TestSwitchToFallbackGroup_OnlyWhenEligible_PG(t *testing.T) {
	SetupTestDB(t)
	_, userA, userB := SeedTestUsers(t)

	// Eligible: fallback set and differs from current group → group moves.
	if err := DB.Model(&User{}).Where("id = ?", userA.Id).
		Updates(map[string]interface{}{"group": "default", "fallback_group": "backup"}).Error; err != nil {
		t.Fatalf("seed A: %v", err)
	}
	if err := SwitchToFallbackGroup(userA.Id); err != nil {
		t.Fatalf("SwitchToFallbackGroup eligible: %v", err)
	}
	var a User
	DB.First(&a, userA.Id)
	if a.Group != "backup" {
		t.Fatalf("eligible user must move to fallback group, got %q", a.Group)
	}

	// Not eligible: no fallback group configured → no-op, group unchanged.
	if err := DB.Model(&User{}).Where("id = ?", userB.Id).
		Updates(map[string]interface{}{"group": "default", "fallback_group": ""}).Error; err != nil {
		t.Fatalf("seed B: %v", err)
	}
	if err := SwitchToFallbackGroup(userB.Id); err != nil {
		t.Fatalf("SwitchToFallbackGroup ineligible: %v", err)
	}
	var b User
	DB.First(&b, userB.Id)
	if b.Group != "default" {
		t.Fatalf("user without fallback must stay in base group, got %q", b.Group)
	}

	// Already-in-fallback: current group == fallback → no-op.
	if err := SwitchToFallbackGroup(userA.Id); err != nil {
		t.Fatalf("SwitchToFallbackGroup already-fallback: %v", err)
	}
	DB.First(&a, userA.Id)
	if a.Group != "backup" {
		t.Fatalf("already-fallback user must stay put, got %q", a.Group)
	}
}

// ─── user_mapping.go GetUserMappingByLurusUserID — platform-account scoping ───

func TestGetUserMappingByLurusUserID_ScopesToTenant_PG(t *testing.T) {
	SetupTestDB(t)
	_, userA, _ := SeedTestUsers(t)

	// Same lurus user linked under two different platform accounts (tenants).
	if _, err := CreateUserMapping(userA.Id, "idp-sub-a", "tenant-a", "a@x.com", "A", "a"); err != nil {
		t.Fatalf("create mapping tenant-a: %v", err)
	}
	if _, err := CreateUserMapping(userA.Id, "idp-sub-b", "tenant-b", "a@x.com", "A", "a"); err != nil {
		t.Fatalf("create mapping tenant-b: %v", err)
	}

	// Lookup scoped to tenant-a returns only the tenant-a mapping (no leak).
	got, err := GetUserMappingByLurusUserID(userA.Id, "tenant-a")
	if err != nil {
		t.Fatalf("GetUserMappingByLurusUserID tenant-a: %v", err)
	}
	if got.TenantID != "tenant-a" || got.IDPSubject != "idp-sub-a" {
		t.Fatalf("cross-account leak: got tenant=%q sub=%q", got.TenantID, got.IDPSubject)
	}

	// A tenant the user is not mapped into returns not-found.
	if _, err := GetUserMappingByLurusUserID(userA.Id, "tenant-c"); err == nil {
		t.Fatal("lookup in unmapped tenant must return not-found")
	}

	// Deactivated mapping is excluded (is_active filter).
	if err := DeactivateUserMapping(got.Id); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if _, err := GetUserMappingByLurusUserID(userA.Id, "tenant-a"); err == nil {
		t.Fatal("deactivated mapping must not be returned")
	}
}

// ─── token.go BatchDeleteTokens — delete count + cross-user isolation ─────────

func TestBatchDeleteTokens_IsolatesByUser_PG(t *testing.T) {
	SetupTestDB(t)
	_, userA, userB := SeedTestUsers(t)

	mk := func(uid int, name string) *Token {
		tok := &Token{UserId: uid, TenantId: "default", Key: common.GetRandomString(30), Status: common.TokenStatusEnabled, Name: name, CreatedTime: common.GetTimestamp(), ExpiredTime: -1, Group: "default"}
		if err := DB.Create(tok).Error; err != nil {
			t.Fatalf("seed token %s: %v", name, err)
		}
		return tok
	}
	a1 := mk(userA.Id, "a1")
	a2 := mk(userA.Id, "a2")
	b1 := mk(userB.Id, "b1")

	// Request deletion of A's two tokens plus B's token, but scoped to userA:
	// only A's rows may be deleted (billing/token isolation).
	n, err := BatchDeleteTokens([]int{a1.Id, a2.Id, b1.Id}, userA.Id)
	if err != nil {
		t.Fatalf("BatchDeleteTokens: %v", err)
	}
	if n != 2 {
		t.Fatalf("must delete exactly A's 2 tokens, got %d", n)
	}

	var remaining int64
	DB.Model(&Token{}).Where("user_id = ?", userA.Id).Count(&remaining)
	if remaining != 0 {
		t.Fatalf("A's tokens must be gone, %d remain", remaining)
	}
	// B's token is untouched — the cross-user id was ignored.
	var bExists int64
	DB.Model(&Token{}).Where("id = ?", b1.Id).Count(&bExists)
	if bExists != 1 {
		t.Fatalf("B's token must survive an A-scoped batch delete")
	}

	// Empty id slice is a validation error.
	if _, err := BatchDeleteTokens(nil, userA.Id); err == nil {
		t.Fatal("empty ids must return an error")
	}
}

// ─── savings.go jsonSourceProductExpr — sqlite (default) dialect branch ───────

func TestJSONSourceProductExpr_SQLiteDefaultBranch(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	common.LogConsumeEnabled = true // allow consume rows through recordLog guards if hit
	start := common.GetTimestamp() - 3600

	// One tagged row + one untagged row (folds into the default bucket).
	seedConsumeLog(t, "gpt-4", 100, 10, 20, `{"source_product":"forge"}`)
	seedConsumeLog(t, "gpt-4", 40, 5, 5, ``)

	rows, err := GetSpendByProduct(start)
	if err != nil {
		t.Fatalf("GetSpendByProduct (sqlite json1 branch): %v", err)
	}
	byProduct := map[string]int64{}
	for _, r := range rows {
		byProduct[r.Product] = r.TotalQuota
	}
	if byProduct["forge"] != 100 {
		t.Fatalf("forge bucket must total 100, got %d (rows=%+v)", byProduct["forge"], rows)
	}
	// The untagged row must fold into exactly one non-forge bucket.
	var foldedTotal int64
	for p, q := range byProduct {
		if p != "forge" {
			foldedTotal += q
		}
	}
	if foldedTotal != 40 {
		t.Fatalf("untagged row must fold into default bucket totalling 40, got %d", foldedTotal)
	}
}
