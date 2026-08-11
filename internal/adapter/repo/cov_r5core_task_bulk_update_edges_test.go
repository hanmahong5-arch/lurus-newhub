package repo

// Covers the "update rows that don't exist" edge case for the three
// TaskBulkUpdate* helpers: the existing sqlite_repo_extra_test.go /
// sqlite_repo_extra2_test.go coverage only exercises the happy path (a
// single matching row). None of them assert on (a) the len(ids)==0
// early-return branch, or (b) IDs that match nothing — GORM's Updates on a
// zero-row WHERE clause is a documented no-op, not an error, but that
// behavior was never actually pinned down here.

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

func r5coreInsertTask(t *testing.T, taskID string) *Task {
	t.Helper()
	task := &Task{
		UserId:     9001,
		TaskID:     taskID,
		Platform:   constant.TaskPlatform("suno"),
		Status:     TaskStatusQueued,
		Progress:   "0%",
		SubmitTime: common.GetTimestamp(),
	}
	if err := task.Insert(); err != nil {
		t.Fatalf("seed Insert: %v", err)
	}
	return task
}

func TestTaskBulkUpdate_EmptyIDSliceIsNoopNotError(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	task := r5coreInsertTask(t, "r5core-bulk-empty")

	if err := TaskBulkUpdate([]string{}, map[string]any{"progress": "99%"}); err != nil {
		t.Fatalf("TaskBulkUpdate(empty ids) should be a no-op, got error: %v", err)
	}
	if err := TaskBulkUpdateByTaskIds([]int64{}, map[string]any{"progress": "99%"}); err != nil {
		t.Fatalf("TaskBulkUpdateByTaskIds(empty ids) should be a no-op, got error: %v", err)
	}
	if err := TaskBulkUpdateByID([]int64{}, map[string]any{"progress": "99%"}); err != nil {
		t.Fatalf("TaskBulkUpdateByID(empty ids) should be a no-op, got error: %v", err)
	}

	got, found, err := GetByTaskId(task.UserId, task.TaskID)
	if err != nil || !found {
		t.Fatalf("reload seeded task: found=%v err=%v", found, err)
	}
	if got.Progress != "0%" {
		t.Errorf("Progress = %q, want unchanged %q — an empty id slice must touch no rows", got.Progress, "0%")
	}
}

func TestTaskBulkUpdate_NonExistentIDsAreNoopNotError(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	task := r5coreInsertTask(t, "r5core-bulk-mismatch")

	if err := TaskBulkUpdate([]string{"task-id-that-does-not-exist"}, map[string]any{"progress": "99%"}); err != nil {
		t.Fatalf("TaskBulkUpdate(unmatched ids) must not error, got: %v", err)
	}
	if err := TaskBulkUpdateByTaskIds([]int64{9999999}, map[string]any{"progress": "99%"}); err != nil {
		t.Fatalf("TaskBulkUpdateByTaskIds(unmatched ids) must not error, got: %v", err)
	}
	if err := TaskBulkUpdateByID([]int64{9999999}, map[string]any{"progress": "99%"}); err != nil {
		t.Fatalf("TaskBulkUpdateByID(unmatched ids) must not error, got: %v", err)
	}

	got, found, err := GetByTaskId(task.UserId, task.TaskID)
	if err != nil || !found {
		t.Fatalf("reload seeded task: found=%v err=%v", found, err)
	}
	if got.Progress != "0%" {
		t.Errorf("Progress = %q, want unchanged %q — an id that matches no row must not touch the seeded row", got.Progress, "0%")
	}
}

func TestTaskBulkUpdate_MatchingIDActuallyUpdates(t *testing.T) {
	// Sanity control so the two no-op assertions above are meaningful: prove
	// the same helpers DO write when the id actually matches.
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	task := r5coreInsertTask(t, "r5core-bulk-match")

	if err := TaskBulkUpdateByID([]int64{task.ID}, map[string]any{"progress": "42%"}); err != nil {
		t.Fatalf("TaskBulkUpdateByID(matching id): %v", err)
	}

	got, found, err := GetByTaskId(task.UserId, task.TaskID)
	if err != nil || !found {
		t.Fatalf("reload seeded task: found=%v err=%v", found, err)
	}
	if got.Progress != "42%" {
		t.Errorf("Progress = %q, want %q after a matching-id bulk update", got.Progress, "42%")
	}
}
