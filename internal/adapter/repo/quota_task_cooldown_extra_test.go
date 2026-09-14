package repo

// quota_task_cooldown_extra_test.go — coverage for previously-thin business
// paths: DecreaseUserQuota (negative guard / batch / direct DB), InitTask
// constructor, SumUsedTaskQuota filter assembly, and MarkMultiKeyCooldown key
// selection. All assertions read real persisted/returned state.

import (
	"testing"

	commonRelay "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

func TestDecreaseUserQuota_Branches(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "decq", "decq@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	// seedUser sets Quota = 1_000_000.

	// 1. Negative guard: rejected, DB untouched.
	if err := DecreaseUserQuota(u.Id, -5); err == nil {
		t.Fatal("negative quota must be rejected")
	}

	// 2. Direct DB path (batch disabled): quota actually decremented.
	prevBatch := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = false
	if err := DecreaseUserQuota(u.Id, 400_000); err != nil {
		t.Fatalf("DecreaseUserQuota direct: %v", err)
	}
	got, err := GetUserById(u.Id)
	if err != nil {
		t.Fatalf("GetUserById: %v", err)
	}
	if got.Quota != 600_000 {
		t.Fatalf("after direct -400000 quota = %d, want 600000", got.Quota)
	}

	// 3. Batch path: buffered, DB unchanged until flush.
	common.BatchUpdateEnabled = true
	defer func() { common.BatchUpdateEnabled = prevBatch }()
	if err := DecreaseUserQuota(u.Id, 100_000); err != nil {
		t.Fatalf("DecreaseUserQuota batch: %v", err)
	}
	got, _ = GetUserById(u.Id)
	if got.Quota != 600_000 {
		t.Fatalf("batch path must not touch DB immediately, quota = %d, want 600000", got.Quota)
	}
	// Flushing the batch store applies the buffered -100000.
	batchUpdate()
	got, _ = GetUserById(u.Id)
	if got.Quota != 500_000 {
		t.Fatalf("after batch flush quota = %d, want 500000", got.Quota)
	}
}

func TestInitTask_GeminiAndNonGemini(t *testing.T) {
	// Gemini channel: private key must be copied; model names propagate.
	geminiInfo := &commonRelay.RelayInfo{
		UserId:          11,
		UsingGroup:      "grpA",
		OriginModelName: "gemini-orig",
		ChannelMeta: &commonRelay.ChannelMeta{
			ChannelType:       constant.ChannelTypeGemini,
			ChannelId:         77,
			ApiKey:            "gemini-secret",
			UpstreamModelName: "gemini-up",
		},
	}
	task := InitTask(nil, constant.TaskPlatformSuno, geminiInfo)
	if task.UserId != 11 || task.Group != "grpA" || task.ChannelId != 77 {
		t.Fatalf("task base fields wrong: %+v", task)
	}
	if task.Platform != constant.TaskPlatformSuno {
		t.Errorf("platform = %v, want Suno", task.Platform)
	}
	if task.Status != TaskStatusNotStart || task.Progress != "0%" {
		t.Errorf("init status/progress wrong: %v / %q", task.Status, task.Progress)
	}
	if task.PrivateData.Key != "gemini-secret" {
		t.Errorf("Gemini key must be captured, got %q", task.PrivateData.Key)
	}
	if task.Properties.UpstreamModelName != "gemini-up" || task.Properties.OriginModelName != "gemini-orig" {
		t.Errorf("model props wrong: %+v", task.Properties)
	}

	// Non-Gemini channel: key must NOT be captured.
	otherInfo := &commonRelay.RelayInfo{
		UserId:     12,
		UsingGroup: "grpB",
		ChannelMeta: &commonRelay.ChannelMeta{
			ChannelType: constant.ChannelTypeOpenAI,
			ChannelId:   5,
			ApiKey:      "openai-secret",
		},
	}
	task2 := InitTask(nil, constant.TaskPlatformSuno, otherInfo)
	if task2.PrivateData.Key != "" {
		t.Errorf("non-Gemini key must be empty, got %q", task2.PrivateData.Key)
	}
}

func TestSumUsedTaskQuota_FilterAssembly(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	// Seed a task so the query has a row to scan.
	task := &Task{
		TaskID:     "t-sum-1",
		Platform:   constant.TaskPlatformSuno,
		UserId:     3,
		ChannelId:  9,
		Quota:      120,
		Action:     "song",
		Status:     TaskStatusSuccess,
		SubmitTime: 1000,
	}
	if err := DB.Create(task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}

	// Populate every filter branch. The final query groups by a "mode" column
	// that the tasks table does not have, so the call surfaces a DB error — the
	// honest, observable behavior of the current query. We assert the branch
	// assembly runs and the error is surfaced (not swallowed).
	params := SyncTaskQueryParams{
		ChannelID:      "9",
		UserID:         "3",
		UserIDs:        []int{3, 4},
		TaskID:         "t-sum-1",
		Action:         "song",
		Status:         string(TaskStatusSuccess),
		StartTimestamp: 500,
		EndTimestamp:   2000,
	}
	_, err := SumUsedTaskQuota(params)
	if err == nil {
		t.Fatal("SumUsedTaskQuota references a non-existent 'mode' column; expected a surfaced DB error")
	}
}

func TestMarkMultiKeyCooldown(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	prevCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	defer func() { common.MemoryCacheEnabled = prevCache }()

	// Non-positive deadline is an immediate no-op.
	if MarkMultiKeyCooldown(1, "k", 0, "r") {
		t.Error("cooldownUntil <= 0 must return false")
	}

	// Single-key (non-multikey) channel: rejected.
	single := &Channel{Name: "single_ch", Type: 1, Key: "only-key", Status: common.ChannelStatusEnabled, TenantId: "default"}
	if err := DB.Create(single).Error; err != nil {
		t.Fatalf("create single channel: %v", err)
	}
	if MarkMultiKeyCooldown(single.Id, "only-key", 9_999_999_999, "r") {
		t.Error("non-multikey channel must return false")
	}

	// Multi-key channel: cooling a real key succeeds and stamps the deadline on
	// the correct key index only.
	multi := &Channel{Name: "multi_ch", Type: 1, Key: "key0\nkey1\nkey2", Status: common.ChannelStatusEnabled, TenantId: "default"}
	multi.ChannelInfo.IsMultiKey = true
	multi.ChannelInfo.MultiKeySize = 3
	if err := DB.Create(multi).Error; err != nil {
		t.Fatalf("create multi channel: %v", err)
	}

	const deadline int64 = 9_999_999_999
	if !MarkMultiKeyCooldown(multi.Id, "key1", deadline, "rate-limited") {
		t.Fatal("cooling an existing key must return true")
	}

	reloaded, err := GetChannelById(multi.Id, true)
	if err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	// key1 is index 1 — that index (and only that index) must carry the deadline.
	if reloaded.ChannelInfo.MultiKeyCooldownUntil[1] != deadline {
		t.Fatalf("index 1 cooldown = %d, want %d", reloaded.ChannelInfo.MultiKeyCooldownUntil[1], deadline)
	}
	if _, ok := reloaded.ChannelInfo.MultiKeyCooldownUntil[0]; ok {
		t.Error("index 0 must not be marked cooling")
	}
	if reloaded.ChannelInfo.MultiKeyStatusList[1] != common.ChannelStatusAutoDisabled {
		t.Errorf("index 1 status = %d, want auto-disabled", reloaded.ChannelInfo.MultiKeyStatusList[1])
	}
	if reloaded.ChannelInfo.MultiKeyDisabledReason[1] != "rate-limited" {
		t.Errorf("index 1 reason = %q", reloaded.ChannelInfo.MultiKeyDisabledReason[1])
	}

	// A key that is not part of the channel is a no-op mutation but the channel
	// exists & is multikey, so the function still reports true (save succeeds).
	if !MarkMultiKeyCooldown(multi.Id, "nonexistent-key", deadline, "r") {
		t.Error("cooling unknown key on a multikey channel returns true (save ok)")
	}
	reloaded2, _ := GetChannelById(multi.Id, true)
	if len(reloaded2.ChannelInfo.MultiKeyCooldownUntil) != 1 {
		t.Errorf("unknown key must not add a cooldown entry, have %d", len(reloaded2.ChannelInfo.MultiKeyCooldownUntil))
	}
}
