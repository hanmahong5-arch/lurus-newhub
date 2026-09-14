package repo

// task_test.go — cycle-8 L8 oracles for the ProjectId/RequestId columns
// migration 035 added: InitTask must copy RelayInfo.ProjectId (the log
// path's own attribution convention) and the live gin request id, and the
// two admin/user task-listing query builders must apply the new
// ProjectID/RequestID filters.

import (
	"net/http/httptest"
	"testing"

	commonRelay "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
)

// TestInitTask_CopiesProjectIdAndRequestId is the mutation oracle for this
// lane: dropping the ProjectId assignment in InitTask must turn this red.
func TestInitTask_CopiesProjectIdAndRequestId(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/tasks/suno", nil)
	c.Set(common.RequestIdKey, "req-init-task-1")

	info := &commonRelay.RelayInfo{
		UserId:     42,
		UsingGroup: "default",
		ProjectId:  99,
		ChannelMeta: &commonRelay.ChannelMeta{
			ChannelType: constant.ChannelTypeOpenAI,
			ChannelId:   7,
		},
	}

	task := InitTask(c, constant.TaskPlatformSuno, info)
	if task.ProjectId != 99 {
		t.Errorf("ProjectId = %d, want 99 (RelayInfo.ProjectId)", task.ProjectId)
	}
	if task.RequestId != "req-init-task-1" {
		t.Errorf("RequestId = %q, want the gin context's request id", task.RequestId)
	}
}

// TestInitTask_NilContext_EmptyRequestId proves InitTask is nil-safe for the
// two non-HTTP callers (repo's own unit tests): no gin.Context means an
// empty RequestId, not a panic.
func TestInitTask_NilContext_EmptyRequestId(t *testing.T) {
	// RelayInfo embeds *ChannelMeta (relay_info.go:198); relayInfo.ChannelId
	// is a promoted field access through that pointer, so it must be
	// non-nil even though this test doesn't care about its contents.
	info := &commonRelay.RelayInfo{UserId: 1, ProjectId: 5, ChannelMeta: &commonRelay.ChannelMeta{}}
	task := InitTask(nil, constant.TaskPlatformSuno, info)
	if task.RequestId != "" {
		t.Errorf("RequestId = %q, want empty with a nil gin.Context", task.RequestId)
	}
	if task.ProjectId != 5 {
		t.Errorf("ProjectId = %d, want 5 (ProjectId does not depend on c)", task.ProjectId)
	}
}

// TestTaskGetAllUserTask_FiltersByProjectAndRequestID and
// TestTaskGetAllTasks_FiltersByProjectAndRequestID prove the two query
// builders repo.SyncTaskQueryParams.ProjectID/RequestID feed actually
// narrow the result set — L10 wires these onto the admin/user list
// handlers, but the builders themselves are this lane's responsibility.
func TestTaskGetAllUserTask_FiltersByProjectAndRequestID(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "task-filter-user", "task-filter@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")

	match := &Task{TaskID: "match-1", Platform: constant.TaskPlatformSuno, UserId: u.Id, ProjectId: 11, RequestId: "req-match", SubmitTime: 1000}
	other := &Task{TaskID: "other-1", Platform: constant.TaskPlatformSuno, UserId: u.Id, ProjectId: 22, RequestId: "req-other", SubmitTime: 1000}
	if err := DB.Create(match).Error; err != nil {
		t.Fatalf("seed match task: %v", err)
	}
	if err := DB.Create(other).Error; err != nil {
		t.Fatalf("seed other task: %v", err)
	}

	byProject := TaskGetAllUserTask(u.Id, 0, 10, SyncTaskQueryParams{ProjectID: "11"})
	if len(byProject) != 1 || byProject[0].TaskID != "match-1" {
		t.Errorf("ProjectID filter = %+v, want exactly [match-1]", byProject)
	}

	byRequest := TaskGetAllUserTask(u.Id, 0, 10, SyncTaskQueryParams{RequestID: "req-other"})
	if len(byRequest) != 1 || byRequest[0].TaskID != "other-1" {
		t.Errorf("RequestID filter = %+v, want exactly [other-1]", byRequest)
	}
}

func TestTaskGetAllTasks_FiltersByProjectAndRequestID(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	match := &Task{TaskID: "admin-match-1", Platform: constant.TaskPlatformSuno, UserId: 1, ProjectId: 33, RequestId: "req-admin-match", SubmitTime: 1000}
	other := &Task{TaskID: "admin-other-1", Platform: constant.TaskPlatformSuno, UserId: 1, ProjectId: 44, RequestId: "req-admin-other", SubmitTime: 1000}
	if err := DB.Create(match).Error; err != nil {
		t.Fatalf("seed match task: %v", err)
	}
	if err := DB.Create(other).Error; err != nil {
		t.Fatalf("seed other task: %v", err)
	}

	byProject := TaskGetAllTasks(0, 10, SyncTaskQueryParams{ProjectID: "33"})
	if len(byProject) != 1 || byProject[0].TaskID != "admin-match-1" {
		t.Errorf("ProjectID filter = %+v, want exactly [admin-match-1]", byProject)
	}

	byRequest := TaskGetAllTasks(0, 10, SyncTaskQueryParams{RequestID: "req-admin-other"})
	if len(byRequest) != 1 || byRequest[0].TaskID != "admin-other-1" {
		t.Errorf("RequestID filter = %+v, want exactly [admin-other-1]", byRequest)
	}
}

// TestTaskRepo_QueryParamsProjectRequestId is the cycle-8 L10 oracle for the
// admin/user COUNT builders: TaskCountAllTasks and TaskCountAllUserTask must
// apply the same ProjectID/RequestID filters TaskGetAllTasks/
// TaskGetAllUserTask already do (above), or a filtered list page reports a
// "total" larger than the rows it actually shows.
func TestTaskRepo_QueryParamsProjectRequestId(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "task-count-filter-user", "task-count-filter@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")

	match := &Task{TaskID: "count-match-1", Platform: constant.TaskPlatformSuno, UserId: u.Id, ProjectId: 77, RequestId: "req-count-match", SubmitTime: 1000}
	other := &Task{TaskID: "count-other-1", Platform: constant.TaskPlatformSuno, UserId: u.Id, ProjectId: 88, RequestId: "req-count-other", SubmitTime: 1000}
	if err := DB.Create(match).Error; err != nil {
		t.Fatalf("seed match task: %v", err)
	}
	if err := DB.Create(other).Error; err != nil {
		t.Fatalf("seed other task: %v", err)
	}

	if got := TaskCountAllUserTask(u.Id, SyncTaskQueryParams{ProjectID: "77"}); got != 1 {
		t.Errorf("TaskCountAllUserTask ProjectID filter = %d, want 1", got)
	}
	if got := TaskCountAllUserTask(u.Id, SyncTaskQueryParams{RequestID: "req-count-other"}); got != 1 {
		t.Errorf("TaskCountAllUserTask RequestID filter = %d, want 1", got)
	}
	if got := TaskCountAllTasks(SyncTaskQueryParams{ProjectID: "77"}); got != 1 {
		t.Errorf("TaskCountAllTasks ProjectID filter = %d, want 1", got)
	}
	if got := TaskCountAllTasks(SyncTaskQueryParams{RequestID: "req-count-other"}); got != 1 {
		t.Errorf("TaskCountAllTasks RequestID filter = %d, want 1", got)
	}
}
