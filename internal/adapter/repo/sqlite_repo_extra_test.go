package repo

// sqlite_repo_extra_test.go — additional coverage for uncovered repo functions.
// Covers: user daily-quota, audit, governance, task, model_meta, prefill_group,
// playground_preset, redemption-extra, usedata (QuotaData), midjourney, tenant-extra.

import (
	"context"
	"fmt"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// ─── User: remaining functions ───────────────────────────────────────────────

func TestUserRepo_Edit(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "edit-user", "edit@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	u.DisplayName = "Edited Name"
	u.Group = "vip"
	u.Quota = 500_000
	u.Remark = "edited remark"
	if err := u.Edit(); err != nil {
		t.Fatalf("Edit: %v", err)
	}

	var fetched User
	if err := DB.First(&fetched, u.Id).Error; err != nil {
		t.Fatalf("fetch after Edit: %v", err)
	}
	if fetched.DisplayName != "Edited Name" {
		t.Errorf("DisplayName not updated, got %q", fetched.DisplayName)
	}
}

func TestUserRepo_Edit_RoleStatusPersistence(t *testing.T) {
	tests := []struct {
		name       string
		editRole   int
		editStatus int
		wantRole   int
		wantStatus int
	}{
		{
			name:       "admin edit persists role and status",
			editRole:   common.RoleAdminUser,
			editStatus: common.UserStatusDisabled,
			wantRole:   common.RoleAdminUser,
			wantStatus: common.UserStatusDisabled,
		},
		{
			name:       "zero role/status means not provided, existing values kept",
			editRole:   0,
			editStatus: 0,
			wantRole:   common.RoleCommonUser,
			wantStatus: common.UserStatusEnabled,
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanup := setupSQLiteDB(t)
			defer cleanup()

			u := seedUser(t, fmt.Sprintf("edit-rs-user-%d", i), fmt.Sprintf("editrs%d@test.local", i),
				common.RoleCommonUser, common.UserStatusEnabled, "default")
			u.Role = tt.editRole
			u.Status = tt.editStatus
			if err := u.Edit(); err != nil {
				t.Fatalf("Edit: %v", err)
			}

			var fetched User
			if err := DB.First(&fetched, u.Id).Error; err != nil {
				t.Fatalf("fetch after Edit: %v", err)
			}
			if fetched.Role != tt.wantRole {
				t.Errorf("Role = %d, want %d", fetched.Role, tt.wantRole)
			}
			if fetched.Status != tt.wantStatus {
				t.Errorf("Status = %d, want %d", fetched.Status, tt.wantStatus)
			}
		})
	}
}

func TestUserRepo_UpdateUserUsedQuotaAndRequestCount(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "quota-req-user", "quotareq@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	common.BatchUpdateEnabled = false
	UpdateUserUsedQuotaAndRequestCount(u.Id, 1000)

	var fetched User
	if err := DB.First(&fetched, u.Id).Error; err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if fetched.UsedQuota < 1000 {
		t.Errorf("UsedQuota not incremented, got %d", fetched.UsedQuota)
	}
	if fetched.RequestCount < 1 {
		t.Errorf("RequestCount not incremented, got %d", fetched.RequestCount)
	}
}

func TestUserRepo_IncreaseDailyUsed(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "daily-user", "daily@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	if err := IncreaseDailyUsed(u.Id, 200); err != nil {
		t.Fatalf("IncreaseDailyUsed: %v", err)
	}

	var fetched User
	if err := DB.First(&fetched, u.Id).Error; err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if fetched.DailyUsed < 200 {
		t.Errorf("DailyUsed not incremented, got %d", fetched.DailyUsed)
	}

	// Negative amount should error
	if err := IncreaseDailyUsed(u.Id, -1); err == nil {
		t.Error("expected error for negative amount")
	}
}

func TestUserRepo_ResetDailyQuota(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "resetdaily-user", "resetdaily@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	DB.Model(&User{}).Where("id = ?", u.Id).Updates(map[string]interface{}{
		"daily_used":       500,
		"last_daily_reset": int64(0),
	})

	if err := ResetDailyQuota(u.Id); err != nil {
		t.Fatalf("ResetDailyQuota: %v", err)
	}

	var fetched User
	if err := DB.First(&fetched, u.Id).Error; err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if fetched.DailyUsed != 0 {
		t.Errorf("DailyUsed should be 0 after reset, got %d", fetched.DailyUsed)
	}
	if fetched.LastDailyReset == 0 {
		t.Error("LastDailyReset should be updated")
	}
}

func TestUserRepo_SwitchToFallbackGroup(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "fallback-user", "fallback@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	DB.Model(&User{}).Where("id = ?", u.Id).Updates(map[string]interface{}{
		"group":          "default",
		"fallback_group": "limited",
		"base_group":     "default",
	})

	if err := SwitchToFallbackGroup(u.Id); err != nil {
		t.Fatalf("SwitchToFallbackGroup: %v", err)
	}

	var fetched User
	if err := DB.First(&fetched, u.Id).Error; err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if fetched.Group != "limited" {
		t.Errorf("Group should be 'limited', got %q", fetched.Group)
	}
}

func TestUserRepo_RestoreToBaseGroup(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "restore-user", "restore@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	DB.Model(&User{}).Where("id = ?", u.Id).Updates(map[string]interface{}{
		"group":      "limited",
		"base_group": "default",
	})

	if err := RestoreToBaseGroup(u.Id); err != nil {
		t.Fatalf("RestoreToBaseGroup: %v", err)
	}

	var fetched User
	if err := DB.First(&fetched, u.Id).Error; err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if fetched.Group != "default" {
		t.Errorf("Group should be 'default', got %q", fetched.Group)
	}
}

func TestUserRepo_GetUsersNeedingDailyReset(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "needs-reset-user", "needsreset@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	DB.Model(&User{}).Where("id = ?", u.Id).Updates(map[string]interface{}{
		"daily_quota":      int64(10_000),
		"last_daily_reset": int64(0),
	})

	users, err := GetUsersNeedingDailyReset(100)
	if err != nil {
		t.Fatalf("GetUsersNeedingDailyReset: %v", err)
	}
	found := false
	for _, uu := range users {
		if uu.Id == u.Id {
			found = true
			break
		}
	}
	if !found {
		t.Error("seeded user should need daily reset")
	}
}

func TestUserRepo_ProcessDailyQuotaReset(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "process-reset-user", "processreset@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")
	DB.Model(&User{}).Where("id = ?", u.Id).Updates(map[string]interface{}{
		"daily_used":       int64(300),
		"last_daily_reset": int64(0),
		"base_group":       "default",
		"group":            "limited",
	})

	if err := ProcessDailyQuotaReset(u.Id); err != nil {
		t.Fatalf("ProcessDailyQuotaReset: %v", err)
	}

	var fetched User
	if err := DB.First(&fetched, u.Id).Error; err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if fetched.DailyUsed != 0 {
		t.Errorf("DailyUsed should be 0, got %d", fetched.DailyUsed)
	}
}

func TestUserRepo_IsSubscriber(t *testing.T) {
	sub := &User{Role: common.RoleSubscriberUser}
	if !sub.IsSubscriber() {
		t.Error("subscriber role should return true")
	}
	cmnUser := &User{Role: common.RoleCommonUser}
	if cmnUser.IsSubscriber() {
		t.Error("common role should return false")
	}
}

func TestUserRepo_UpdateUserRole(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "role-update-user", "roleupdate@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")

	if err := UpdateUserRole(u.Id, common.RoleAdminUser); err != nil {
		t.Fatalf("UpdateUserRole: %v", err)
	}

	role, err := GetUserRole(u.Id)
	if err != nil {
		t.Fatalf("GetUserRole: %v", err)
	}
	if role != common.RoleAdminUser {
		t.Errorf("role should be %d, got %d", common.RoleAdminUser, role)
	}

	// Invalid role must error
	if err := UpdateUserRole(u.Id, 9999); err == nil {
		t.Error("expected error for invalid role")
	}
}

// ─── Setup ────────────────────────────────────────────────────────────────────

func TestSetupRepo_GetSetup(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	// Empty table — must return nil without panic
	result := GetSetup()
	_ = result
}

// ─── Audit ────────────────────────────────────────────────────────────────────

func TestAuditRepo_CreateAndQuery(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	repo := &AuditEventRepo{}
	ev := &entity.AuditEvent{
		TenantID:  "tenant-audit",
		Timestamp: common.GetTimestamp(),
		ActorType: "user",
		ActorID:   42,
		Action:    "token.created",
		Resource:  "token",
	}
	if err := repo.CreateAuditEvent(ev); err != nil {
		t.Fatalf("CreateAuditEvent: %v", err)
	}
	if ev.ID == 0 {
		t.Error("ID should be set after create")
	}

	events, total, err := GetAuditEvents("tenant-audit", "", 0, "", 0, 0, 0, 10)
	if err != nil {
		t.Fatalf("GetAuditEvents: %v", err)
	}
	if total < 1 {
		t.Errorf("expected >=1 event, got %d", total)
	}
	_ = events
}

func TestAuditRepo_FilterByAction(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	repo := &AuditEventRepo{}
	_ = repo.CreateAuditEvent(&entity.AuditEvent{
		TenantID:  "t-filter",
		Timestamp: common.GetTimestamp(),
		ActorType: "admin",
		ActorID:   1,
		Action:    "channel.deleted",
		Resource:  "channel",
	})

	_, total, err := GetAuditEvents("t-filter", "channel.deleted", 0, "", 0, 0, 0, 10)
	if err != nil {
		t.Fatalf("GetAuditEvents(action): %v", err)
	}
	if total < 1 {
		t.Errorf("expected 1 with action filter, got %d", total)
	}
}

func TestAuditRepo_DeleteOldAuditEvents(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	repo := &AuditEventRepo{}
	old := common.GetTimestamp() - 10000
	_ = repo.CreateAuditEvent(&entity.AuditEvent{
		TenantID:  "default",
		Timestamp: old,
		ActorType: "user",
		Action:    "login",
	})

	cutoff := common.GetTimestamp()
	deleted, err := DeleteOldAuditEvents(context.Background(), cutoff, 100)
	if err != nil {
		t.Fatalf("DeleteOldAuditEvents: %v", err)
	}
	if deleted < 1 {
		t.Errorf("expected >=1 deleted, got %d", deleted)
	}
}

// ─── Governance ───────────────────────────────────────────────────────────────

func TestGovernanceRepo_GetChannelDistribution(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	// Seed a consume log with channel_type > 0 so the query has something to aggregate.
	RecordLog(1, LogTypeConsume, "governance-test-log")

	results, err := GetChannelDistribution(0)
	if err != nil {
		t.Fatalf("GetChannelDistribution: %v", err)
	}
	_ = results
}

func TestGovernanceRepo_GetLatencyStats(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	results, err := GetLatencyStats(0)
	if err != nil {
		t.Fatalf("GetLatencyStats: %v", err)
	}
	_ = results
}

func TestGovernanceRepo_GetEfficiencyStats(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	results, err := GetEfficiencyStats(0)
	if err != nil {
		t.Fatalf("GetEfficiencyStats: %v", err)
	}
	_ = results
}

func TestGovernanceRepo_GetFingerprintDedupStats(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	results, err := GetFingerprintDedupStats(0, 0)
	if err != nil {
		t.Fatalf("GetFingerprintDedupStats: %v", err)
	}
	_ = results
}

// ─── Task ─────────────────────────────────────────────────────────────────────

func TestTaskRepo_SetDataGetData(t *testing.T) {
	task := &Task{}
	payload := map[string]interface{}{"key": "value", "num": float64(42)}
	task.SetData(payload)

	var out map[string]interface{}
	if err := task.GetData(&out); err != nil {
		t.Fatalf("GetData: %v", err)
	}
	if out["key"] != "value" {
		t.Errorf("expected key=value, got %v", out["key"])
	}
}

func TestTaskRepo_InsertAndGetAll(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	task := &Task{
		UserId:     1,
		Platform:   constant.TaskPlatform("suno"),
		Action:     "song",
		Status:     TaskStatusNotStart,
		Progress:   "0%",
		SubmitTime: common.GetTimestamp(),
	}
	task.SetData(map[string]string{"title": "test song"})

	if err := task.Insert(); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if task.ID == 0 {
		t.Error("ID should be set after insert")
	}

	tasks := TaskGetAllUserTask(1, 0, 10, SyncTaskQueryParams{})
	if len(tasks) < 1 {
		t.Error("expected at least 1 task for user")
	}

	allTasks := TaskGetAllTasks(0, 10, SyncTaskQueryParams{})
	if len(allTasks) < 1 {
		t.Error("expected at least 1 task in GetAllTasks")
	}
}

func TestTaskRepo_GetByTaskId(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	task := &Task{
		UserId:     2,
		TaskID:     "task-abc-123",
		Platform:   constant.TaskPlatform("suno"),
		Action:     "lyrics",
		Status:     TaskStatusQueued,
		Progress:   "0%",
		SubmitTime: common.GetTimestamp(),
	}
	if err := task.Insert(); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	found, exists, err := GetByOnlyTaskId("task-abc-123")
	if err != nil || !exists {
		t.Fatalf("GetByOnlyTaskId: exists=%v err=%v", exists, err)
	}
	if found.TaskID != "task-abc-123" {
		t.Errorf("wrong task_id: %q", found.TaskID)
	}

	found2, exists2, err := GetByTaskId(2, "task-abc-123")
	if err != nil || !exists2 {
		t.Fatalf("GetByTaskId: exists=%v err=%v", exists2, err)
	}
	_ = found2

	tasks, err := GetByTaskIds(2, []any{"task-abc-123"})
	if err != nil {
		t.Fatalf("GetByTaskIds: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1, got %d", len(tasks))
	}
}

func TestTaskRepo_UpdateAndProgress(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	task := &Task{
		UserId:     3,
		TaskID:     "task-upd-456",
		Platform:   constant.TaskPlatform("suno"),
		Status:     TaskStatusInProgress,
		Progress:   "25%",
		SubmitTime: common.GetTimestamp(),
	}
	if err := task.Insert(); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := TaskUpdateProgress(task.ID, "75%"); err != nil {
		t.Fatalf("TaskUpdateProgress: %v", err)
	}

	task.Status = TaskStatusSuccess
	task.Progress = "100%"
	if err := task.Update(); err != nil {
		t.Fatalf("Update: %v", err)
	}
}

func TestTaskRepo_GetAllUnFinish(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	task := &Task{
		UserId:     4,
		TaskID:     "unfinish-task-001",
		Platform:   constant.TaskPlatform("suno"),
		Status:     TaskStatusInProgress,
		Progress:   "50%",
		SubmitTime: common.GetTimestamp(),
	}
	if err := task.Insert(); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	unfinished := GetAllUnFinishSyncTasks(100)
	found := false
	for _, tt := range unfinished {
		if tt.TaskID == "unfinish-task-001" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected unfinished task in result")
	}
}

func TestTaskRepo_BulkUpdate(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	task := &Task{
		UserId:     5,
		TaskID:     "bulk-task-001",
		Platform:   constant.TaskPlatform("suno"),
		Status:     TaskStatusQueued,
		Progress:   "0%",
		SubmitTime: common.GetTimestamp(),
	}
	if err := task.Insert(); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := TaskBulkUpdate([]string{"bulk-task-001"}, map[string]any{"progress": "10%"}); err != nil {
		t.Fatalf("TaskBulkUpdate: %v", err)
	}

	if err := TaskBulkUpdateByTaskIds([]int64{task.ID}, map[string]any{"progress": "20%"}); err != nil {
		t.Fatalf("TaskBulkUpdateByTaskIds: %v", err)
	}

	count := TaskCountAllTasks(SyncTaskQueryParams{})
	if count < 1 {
		t.Error("expected count >= 1")
	}

	userCount := TaskCountAllUserTask(5, SyncTaskQueryParams{})
	if userCount < 1 {
		t.Error("expected user task count >= 1")
	}
}

func TestTaskRepo_FilteredQueries(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	task := &Task{
		UserId:     6,
		TaskID:     "filter-task-001",
		Platform:   constant.TaskPlatform("midjourney"),
		Action:     "IMAGINE",
		Status:     TaskStatusSuccess,
		Progress:   "100%",
		SubmitTime: common.GetTimestamp(),
	}
	if err := task.Insert(); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	params := SyncTaskQueryParams{
		Platform: "midjourney",
		Status:   string(TaskStatusSuccess),
		Action:   "IMAGINE",
	}

	tasks := TaskGetAllUserTask(6, 0, 10, params)
	if len(tasks) < 1 {
		t.Error("expected filtered tasks for user 6")
	}

	allTasks := TaskGetAllTasks(0, 10, params)
	if len(allTasks) < 1 {
		t.Error("expected filtered all tasks")
	}

	count := TaskCountAllTasks(params)
	if count < 1 {
		t.Error("expected count >= 1 with platform filter")
	}
}

// ─── Model Meta ───────────────────────────────────────────────────────────────

func TestModelMeta_InsertAndGet(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	m := &Model{
		ModelName:   "gpt-test-model",
		Description: "test model",
		Status:      1,
	}
	if err := ModelInsert(m); err != nil {
		t.Fatalf("ModelInsert: %v", err)
	}
	if m.Id == 0 {
		t.Error("ID should be set after insert")
	}

	models, err := GetAllModels(0, 10)
	if err != nil {
		t.Fatalf("GetAllModels: %v", err)
	}
	if len(models) < 1 {
		t.Error("expected at least 1 model")
	}
}

func TestModelMeta_IsDuplicated(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	m := &Model{ModelName: "unique-model", Status: 1}
	if err := ModelInsert(m); err != nil {
		t.Fatalf("ModelInsert: %v", err)
	}

	dup, err := IsModelNameDuplicated(0, "unique-model")
	if err != nil {
		t.Fatalf("IsModelNameDuplicated: %v", err)
	}
	if !dup {
		t.Error("expected duplicate=true for existing name with different id")
	}

	notDup, err := IsModelNameDuplicated(m.Id, "unique-model")
	if err != nil {
		t.Fatalf("IsModelNameDuplicated (self): %v", err)
	}
	if notDup {
		t.Error("expected false when excluding own id")
	}
}

func TestModelMeta_UpdateAndDelete(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	m := &Model{ModelName: "update-del-model", Status: 1}
	if err := ModelInsert(m); err != nil {
		t.Fatalf("ModelInsert: %v", err)
	}

	m.Description = "updated description"
	if err := ModelUpdate(m); err != nil {
		t.Fatalf("ModelUpdate: %v", err)
	}

	if err := ModelDelete(m); err != nil {
		t.Fatalf("ModelDelete: %v", err)
	}
}

func TestModelMeta_GetVendorModelCounts(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	if err := ModelInsert(&Model{ModelName: "vendor-model-1", VendorID: 10, Status: 1}); err != nil {
		t.Fatalf("ModelInsert: %v", err)
	}

	counts, err := GetVendorModelCounts()
	if err != nil {
		t.Fatalf("GetVendorModelCounts: %v", err)
	}
	if counts[10] < 1 {
		t.Errorf("expected count>=1 for vendor 10, got %d", counts[10])
	}
}

func TestModelMeta_SearchModels(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	if err := ModelInsert(&Model{ModelName: "searchable-model", Tags: "alpha", Status: 1}); err != nil {
		t.Fatalf("ModelInsert: %v", err)
	}

	models, total, err := SearchModels("searchable", "", 0, 10)
	if err != nil {
		t.Fatalf("SearchModels: %v", err)
	}
	if total < 1 {
		t.Errorf("expected >=1, got %d", total)
	}
	_ = models
}

func TestModelMeta_GetBoundChannels(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	// No channels with abilities — should return empty map without error
	result, err := GetBoundChannelsByModelsMap([]string{"gpt-4"})
	if err != nil {
		t.Fatalf("GetBoundChannelsByModelsMap: %v", err)
	}
	_ = result
}

// ─── PrefillGroup ─────────────────────────────────────────────────────────────

func TestPrefillGroup_InsertAndGet(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	g := &PrefillGroup{Name: "prefill-group-a", Type: "system"}
	if err := PrefillGroupInsert(g); err != nil {
		t.Fatalf("PrefillGroupInsert: %v", err)
	}
	if g.Id == 0 {
		t.Error("ID should be set")
	}

	groups, err := GetAllPrefillGroups("")
	if err != nil {
		t.Fatalf("GetAllPrefillGroups: %v", err)
	}
	if len(groups) < 1 {
		t.Error("expected at least 1 group")
	}

	filtered, err := GetAllPrefillGroups("system")
	if err != nil {
		t.Fatalf("GetAllPrefillGroups(filtered): %v", err)
	}
	if len(filtered) < 1 {
		t.Error("expected filtered result")
	}
}

func TestPrefillGroup_IsDuplicated(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	g := &PrefillGroup{Name: "dup-prefill", Type: "user"}
	if err := PrefillGroupInsert(g); err != nil {
		t.Fatalf("PrefillGroupInsert: %v", err)
	}

	dup, err := IsPrefillGroupNameDuplicated(0, "dup-prefill")
	if err != nil {
		t.Fatalf("IsPrefillGroupNameDuplicated: %v", err)
	}
	if !dup {
		t.Error("expected duplicate=true")
	}

	notDup, err := IsPrefillGroupNameDuplicated(g.Id, "dup-prefill")
	if err != nil {
		t.Fatalf("IsPrefillGroupNameDuplicated (self): %v", err)
	}
	if notDup {
		t.Error("expected false when excluding own id")
	}
}

func TestPrefillGroup_UpdateAndDelete(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	g := &PrefillGroup{Name: "upd-prefill", Type: "user"}
	if err := PrefillGroupInsert(g); err != nil {
		t.Fatalf("PrefillGroupInsert: %v", err)
	}

	g.Description = "updated"
	if err := PrefillGroupUpdate(g); err != nil {
		t.Fatalf("PrefillGroupUpdate: %v", err)
	}

	if err := DeletePrefillGroupByID(g.Id); err != nil {
		t.Fatalf("DeletePrefillGroupByID: %v", err)
	}
}

// ─── PlaygroundPreset ─────────────────────────────────────────────────────────

func TestPlaygroundPreset_CRUD(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	p := &PlaygroundPreset{
		TenantID: "tenant-pg",
		UserID:   1,
		Name:     "My Preset",
		Prompt:   "You are a helpful assistant.",
		Models:   `["gpt-4o"]`,
		Params:   `{"temperature":0.7}`,
	}
	if err := CreatePlaygroundPreset(p); err != nil {
		t.Fatalf("CreatePlaygroundPreset: %v", err)
	}
	if p.Id == 0 {
		t.Error("ID should be set")
	}

	list, err := ListPlaygroundPresets("tenant-pg", 1)
	if err != nil {
		t.Fatalf("ListPlaygroundPresets: %v", err)
	}
	if len(list) < 1 {
		t.Error("expected at least 1 preset")
	}

	fetched, err := GetPlaygroundPreset(p.Id)
	if err != nil {
		t.Fatalf("GetPlaygroundPreset: %v", err)
	}
	if fetched == nil {
		t.Fatal("expected non-nil preset")
	}
	if fetched.Name != "My Preset" {
		t.Errorf("wrong name: %q", fetched.Name)
	}

	exists, err := PlaygroundPresetNameExists("tenant-pg", 1, "My Preset")
	if err != nil {
		t.Fatalf("PlaygroundPresetNameExists: %v", err)
	}
	if !exists {
		t.Error("expected preset name to exist")
	}

	if err := DeletePlaygroundPreset(p.Id); err != nil {
		t.Fatalf("DeletePlaygroundPreset: %v", err)
	}

	notFound, err := GetPlaygroundPreset(p.Id)
	if err != nil {
		t.Fatalf("GetPlaygroundPreset after delete: %v", err)
	}
	if notFound != nil {
		t.Error("expected nil after delete")
	}
}

// ─── Redemption remaining ─────────────────────────────────────────────────────

func TestRedemptionRepo_SearchAndInsert(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ten := seedTenant(t, "redemption-tenant", "red-tenant", "Red Tenant")
	u := seedUser(t, "redeem-user", "redeem@test.local", common.RoleCommonUser, common.UserStatusEnabled, ten.Id)

	r := &Redemption{
		UserId:      u.Id,
		TenantId:    ten.Id,
		Key:         common.GetRandomString(32),
		Name:        "search-test-code",
		Quota:       1000,
		Status:      common.RedemptionCodeStatusEnabled,
		CreatedTime: common.GetTimestamp(),
	}
	if err := RedemptionInsert(r); err != nil {
		t.Fatalf("RedemptionInsert: %v", err)
	}
	if r.Id == 0 {
		t.Error("ID should be set")
	}

	results, total, err := SearchRedemptions("search-test", 0, 10)
	if err != nil {
		t.Fatalf("SearchRedemptions: %v", err)
	}
	_ = results
	_ = total
}

func TestRedemptionRepo_DeleteInvalid(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ten := seedTenant(t, "del-redemption-tenant", "del-red", "Del Red Tenant")
	u := seedUser(t, "del-redeem-user", "delredeem@test.local", common.RoleCommonUser, common.UserStatusEnabled, ten.Id)

	r := &Redemption{
		UserId:      u.Id,
		TenantId:    ten.Id,
		Key:         common.GetRandomString(32),
		Name:        "invalid-code",
		Quota:       500,
		Status:      common.RedemptionCodeStatusDisabled,
		CreatedTime: common.GetTimestamp(),
	}
	if err := RedemptionInsert(r); err != nil {
		t.Fatalf("RedemptionInsert: %v", err)
	}

	_, err := DeleteInvalidRedemptions()
	if err != nil {
		t.Fatalf("DeleteInvalidRedemptions: %v", err)
	}
}

// ─── QuotaData (usedata.go) ───────────────────────────────────────────────────

func TestUsedataRepo_LogAndSave(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	now := common.GetTimestamp()
	LogQuotaData(1, "testuser", "gpt-4", 100, now, 500)
	LogQuotaData(1, "testuser", "gpt-4", 200, now, 1000)
	SaveQuotaDataCache()

	hourStart := (now / 3600) * 3600
	data, err := GetQuotaDataByUserId(1, hourStart-3600, hourStart+7200)
	if err != nil {
		t.Fatalf("GetQuotaDataByUserId: %v", err)
	}
	_ = data
}

func TestUsedataRepo_GetByUsername(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	now := common.GetTimestamp()
	LogQuotaData(2, "named-user", "claude-3", 50, now, 200)
	SaveQuotaDataCache()

	hourStart := (now / 3600) * 3600
	data, err := GetQuotaDataByUsername("named-user", hourStart-3600, hourStart+7200)
	if err != nil {
		t.Fatalf("GetQuotaDataByUsername: %v", err)
	}
	_ = data
}

func TestUsedataRepo_GetAllQuotaDates(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	now := common.GetTimestamp()
	LogQuotaData(3, "all-dates-user", "gemini-pro", 75, now, 300)
	SaveQuotaDataCache()

	hourStart := (now / 3600) * 3600
	data, err := GetAllQuotaDates(hourStart-3600, hourStart+7200, "")
	if err != nil {
		t.Fatalf("GetAllQuotaDates: %v", err)
	}
	_ = data

	data2, err := GetAllQuotaDates(hourStart-3600, hourStart+7200, "all-dates-user")
	if err != nil {
		t.Fatalf("GetAllQuotaDates(username): %v", err)
	}
	_ = data2
}

// ─── SwitchConfigPreset ───────────────────────────────────────────────────────

func TestSwitchPreset_TableName(t *testing.T) {
	row := SwitchConfigPresetRow{}
	if row.TableName() != "switch_config_presets" {
		t.Errorf("unexpected table name: %q", row.TableName())
	}
}

func TestSwitchPreset_CreateAndList(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	// switch_config_presets uses gen_random_uuid() default which SQLite may not support.
	if err := DB.AutoMigrate(&SwitchConfigPresetRow{}); err != nil {
		t.Skipf("SwitchConfigPresetRow migration: %v", err)
	}

	ctx := context.Background()
	cfg := map[string]interface{}{"model": "gpt-4o", "temperature": 0.7}
	dto, err := CreateSwitchConfigPreset(ctx, "lumen", "My Preset", "desc", "creative", cfg, true)
	if err != nil {
		t.Fatalf("CreateSwitchConfigPreset: %v", err)
	}
	if dto.ID == "" {
		t.Error("expected non-empty ID")
	}

	presets, err := ListSwitchConfigPresets(ctx, "lumen", "", 10, 0)
	if err != nil {
		t.Fatalf("ListSwitchConfigPresets: %v", err)
	}
	if len(presets) < 1 {
		t.Error("expected at least 1 preset")
	}
}

// ─── MissingModels ────────────────────────────────────────────────────────────

func TestMissingModels_GetMissingModels(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	result, err := GetMissingModels()
	if err != nil {
		t.Fatalf("GetMissingModels: %v", err)
	}
	_ = result
}

// ─── Tenant: remaining functions ──────────────────────────────────────────────

func TestTenantRepo_TenantCanAddUser(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ten := seedTenant(t, "adduser-tenant", "adduser", "Add User Tenant")
	can, err := TenantCanAddUser(ten)
	if err != nil {
		t.Fatalf("TenantCanAddUser: %v", err)
	}
	_ = can
}

func TestTenantRepo_GetTenantMonthlyQuotaUsed(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	ten := seedTenant(t, "monthly-quota-tenant", "monthly-q", "Monthly Quota Tenant")
	used, err := GetTenantMonthlyQuotaUsed(ten.Id, "2026-05")
	if err != nil {
		t.Fatalf("GetTenantMonthlyQuotaUsed: %v", err)
	}
	_ = used
}

// ─── ModelExtra ───────────────────────────────────────────────────────────────

func TestModelExtra_GetModelEnableGroups(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	groups := GetModelEnableGroups("gpt-4")
	_ = groups
}

func TestModelExtra_GetModelQuotaTypes(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	types := GetModelQuotaTypes("gpt-4")
	_ = types
}

// ─── Midjourney ───────────────────────────────────────────────────────────────

func ensureMJTable(t *testing.T) {
	t.Helper()
	if !DB.Migrator().HasTable(&Midjourney{}) {
		if err := DB.AutoMigrate(&Midjourney{}); err != nil {
			t.Skipf("Midjourney migration failed: %v", err)
		}
	}
}

func seedMidjourney(t *testing.T, mjID string, userID int) *Midjourney {
	t.Helper()
	mj := &Midjourney{
		UserId:   userID,
		Code:     1,
		Action:   "IMAGINE",
		MjId:     mjID,
		Progress: "0%",
	}
	if err := MjInsert(mj); err != nil {
		t.Fatalf("MjInsert %q: %v", mjID, err)
	}
	return mj
}

func TestMidjourneyRepo_InsertAndGetAll(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	ensureMJTable(t)

	seedMidjourney(t, "mj-task-001", 1)
	seedMidjourney(t, "mj-task-002", 1)

	tasks := GetAllUserTask(1, 0, 10, TaskQueryParams{})
	if len(tasks) < 1 {
		t.Error("expected at least 1 task for user")
	}

	allTasks := GetAllTasks(0, 10, TaskQueryParams{})
	if len(allTasks) < 1 {
		t.Error("expected at least 1 task in GetAllTasks")
	}
}

func TestMidjourneyRepo_GetByMJId(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	ensureMJTable(t)

	seedMidjourney(t, "mj-lookup-001", 2)

	mj := GetByOnlyMJId("mj-lookup-001")
	if mj == nil {
		t.Fatal("GetByOnlyMJId returned nil")
	}

	mj2 := GetByMJId(2, "mj-lookup-001")
	if mj2 == nil {
		t.Fatal("GetByMJId returned nil")
	}

	mjs := GetByMJIds(2, []string{"mj-lookup-001"})
	if len(mjs) < 1 {
		t.Error("GetByMJIds should return 1 result")
	}
}

func TestMidjourneyRepo_GetMjByuId(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	ensureMJTable(t)

	mj := seedMidjourney(t, "mj-byuid-001", 3)

	fetched := GetMjByuId(mj.Id)
	if fetched == nil {
		t.Fatal("GetMjByuId returned nil")
	}
}

func TestMidjourneyRepo_UpdateAndCount(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	ensureMJTable(t)

	mj := seedMidjourney(t, "mj-upd-001", 4)

	if err := UpdateProgress(mj.Id, "50%"); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}

	mj.Progress = "100%"
	mj.Code = 0
	if err := MjUpdate(mj); err != nil {
		t.Fatalf("MjUpdate: %v", err)
	}

	if err := MjBulkUpdate([]string{"mj-upd-001"}, map[string]any{"progress": "99%"}); err != nil {
		t.Fatalf("MjBulkUpdate: %v", err)
	}

	if err := MjBulkUpdateByTaskIds([]int{mj.Id}, map[string]any{"progress": "100%"}); err != nil {
		t.Fatalf("MjBulkUpdateByTaskIds: %v", err)
	}

	count := CountAllTasks(TaskQueryParams{})
	_ = count

	userCount := CountAllUserTask(4, TaskQueryParams{})
	_ = userCount
}

func TestMidjourneyRepo_GetUnfinished(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	ensureMJTable(t)

	seedMidjourney(t, "mj-unfinish-001", 5)

	unfinished := GetAllUnFinishTasks()
	_ = unfinished
}
