package repo

import (
	"encoding/json"
	"time"

	commonRelay "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	entity "github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

// Subtypes aliased from the canonical definition in domain/entity/task.go
type TaskStatus = entity.TaskStatus
type Properties = entity.Properties
type TaskPrivateData = entity.TaskPrivateData
type SyncTaskQueryParams = entity.SyncTaskQueryParams
type TaskQuotaUsage = entity.TaskQuotaUsage

// Re-export TaskStatus constants from entity
const (
	TaskStatusNotStart   = entity.TaskStatusNotStart
	TaskStatusSubmitted  = entity.TaskStatusSubmitted
	TaskStatusQueued     = entity.TaskStatusQueued
	TaskStatusInProgress = entity.TaskStatusInProgress
	TaskStatusFailure    = entity.TaskStatusFailure
	TaskStatusSuccess    = entity.TaskStatusSuccess
	TaskStatusUnknown    = entity.TaskStatusUnknown
)

// Canonical definition: domain/entity/task.go
type Task struct {
	ID         int64                 `json:"id" gorm:"primary_key;AUTO_INCREMENT"`
	CreatedAt  int64                 `json:"created_at" gorm:"index"`
	UpdatedAt  int64                 `json:"updated_at"`
	TaskID     string                `json:"task_id" gorm:"type:varchar(191);index"` // 第三方id，不一定有/ song id\ Task id
	Platform   constant.TaskPlatform `json:"platform" gorm:"type:varchar(30);index"` // 平台
	UserId     int                   `json:"user_id" gorm:"index"`
	Group      string                `json:"group" gorm:"type:varchar(50)"` // 修正计费用
	ChannelId  int                   `json:"channel_id" gorm:"index"`
	Quota      int                   `json:"quota"`
	Action     string                `json:"action" gorm:"type:varchar(40);index"` // 任务类型, song, lyrics, description-mode
	Status     TaskStatus            `json:"status" gorm:"type:varchar(20);index"` // 任务状态
	FailReason string                `json:"fail_reason"`
	SubmitTime int64                 `json:"submit_time" gorm:"index"`
	StartTime  int64                 `json:"start_time" gorm:"index"`
	FinishTime int64                 `json:"finish_time" gorm:"index"`
	Progress   string                `json:"progress" gorm:"type:varchar(20);index"`
	Properties Properties            `json:"properties" gorm:"type:json"`
	// 禁止返回给用户，内部可能包含key等隐私信息
	PrivateData TaskPrivateData `json:"-" gorm:"column:private_data;type:json"`
	Data        json.RawMessage `json:"data" gorm:"type:json"`
	// ProjectId/RequestId: see the identical-tag convention comment on
	// entity.Task (domain/entity/task.go) — this is the struct AutoMigrate
	// actually runs against (repo/main.go migrateDB registers &Task{}, this
	// package's own type, not entity.Task).
	ProjectId int    `json:"project_id" gorm:"not null;default:0"`
	RequestId string `json:"request_id" gorm:"type:varchar(64);not null;default:'';index"`
}

func (t *Task) SetData(data any) {
	b, _ := json.Marshal(data)
	t.Data = json.RawMessage(b)
}

func (t *Task) GetData(v any) error {
	err := json.Unmarshal(t.Data, &v)
	return err
}

// InitTask builds the initial row for a newly-submitted async task.
//
// c carries the caller's live gin.Context so the task can be tagged with
// the request id (common.RequestIdKey) it was submitted under — a
// support/cost-attribution lookup key distinct from the upstream vendor's
// own TaskID. It is optional (nil-safe): callers without a live request in
// flight (currently only this repo's own unit tests) pass nil and get an
// empty RequestId, matching the pre-migration-035 behaviour.
func InitTask(c *gin.Context, platform constant.TaskPlatform, relayInfo *commonRelay.RelayInfo) *Task {
	properties := Properties{}
	privateData := TaskPrivateData{}
	if relayInfo != nil && relayInfo.ChannelMeta != nil {
		if relayInfo.ChannelMeta.ChannelType == constant.ChannelTypeGemini {
			privateData.Key = relayInfo.ChannelMeta.ApiKey
		}
		if relayInfo.UpstreamModelName != "" {
			properties.UpstreamModelName = relayInfo.UpstreamModelName
		}
		if relayInfo.OriginModelName != "" {
			properties.OriginModelName = relayInfo.OriginModelName
		}
	}

	t := &Task{
		UserId:      relayInfo.UserId,
		Group:       relayInfo.UsingGroup,
		SubmitTime:  time.Now().Unix(),
		Status:      TaskStatusNotStart,
		Progress:    "0%",
		ChannelId:   relayInfo.ChannelId,
		Platform:    platform,
		Properties:  properties,
		PrivateData: privateData,
		// ProjectId mirrors the log path's convention (SetupContextForToken
		// -> RelayInfo.ProjectId -> RecordConsumeLogParams.ProjectId): the
		// authenticated token's cost-attribution project, 0 when unassigned.
		ProjectId: relayInfo.ProjectId,
	}
	if c != nil {
		t.RequestId = c.GetString(common.RequestIdKey)
	}
	return t
}

func TaskGetAllUserTask(userId int, startIdx int, num int, queryParams SyncTaskQueryParams) []*Task {
	var tasks []*Task
	var err error

	// 初始化查询构建器
	query := DB.Where("user_id = ?", userId)

	if queryParams.TaskID != "" {
		query = query.Where("task_id = ?", queryParams.TaskID)
	}
	if queryParams.Action != "" {
		query = query.Where("action = ?", queryParams.Action)
	}
	if queryParams.Status != "" {
		query = query.Where("status = ?", queryParams.Status)
	}
	if queryParams.Platform != "" {
		query = query.Where("platform = ?", queryParams.Platform)
	}
	if queryParams.ProjectID != "" {
		query = query.Where("project_id = ?", queryParams.ProjectID)
	}
	if queryParams.RequestID != "" {
		query = query.Where("request_id = ?", queryParams.RequestID)
	}
	if queryParams.StartTimestamp != 0 {
		// 假设您已将前端传来的时间戳转换为数据库所需的时间格式，并处理了时间戳的验证和解析
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != 0 {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}

	// 获取数据
	err = query.Omit("channel_id").Order("id desc").Limit(num).Offset(startIdx).Find(&tasks).Error
	if err != nil {
		return nil
	}

	return tasks
}

func TaskGetAllTasks(startIdx int, num int, queryParams SyncTaskQueryParams) []*Task {
	var tasks []*Task
	var err error

	// 初始化查询构建器
	query := DB

	// 添加过滤条件
	if queryParams.ChannelID != "" {
		query = query.Where("channel_id = ?", queryParams.ChannelID)
	}
	if queryParams.Platform != "" {
		query = query.Where("platform = ?", queryParams.Platform)
	}
	if queryParams.UserID != "" {
		query = query.Where("user_id = ?", queryParams.UserID)
	}
	if len(queryParams.UserIDs) != 0 {
		query = query.Where("user_id in (?)", queryParams.UserIDs)
	}
	if queryParams.TaskID != "" {
		query = query.Where("task_id = ?", queryParams.TaskID)
	}
	if queryParams.Action != "" {
		query = query.Where("action = ?", queryParams.Action)
	}
	if queryParams.Status != "" {
		query = query.Where("status = ?", queryParams.Status)
	}
	if queryParams.ProjectID != "" {
		query = query.Where("project_id = ?", queryParams.ProjectID)
	}
	if queryParams.RequestID != "" {
		query = query.Where("request_id = ?", queryParams.RequestID)
	}
	if queryParams.StartTimestamp != 0 {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != 0 {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}
	// TenantID: this table has no tenant_id column of its own (unlike
	// channel/user/redemption), so the scope reaches the tenant through the
	// owning user row, same precedent as applyQuotaData (repo/usedata.go).
	// GetAllTask only sets TenantScoped for non-root admins. The gate is
	// TenantScoped, not TenantID != "" — a non-root caller with no tenant on
	// its session (TenantID == "") still gets the WHERE clause, and an empty
	// string matches no user row, so the page comes back empty instead of
	// falling through to every tenant's rows (fail-closed, operator
	// decision cycle-11 L8 repair).
	if queryParams.TenantScoped {
		query = query.Where("user_id IN (SELECT id FROM users WHERE tenant_id = ?)", queryParams.TenantID)
	}

	// 获取数据
	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&tasks).Error
	if err != nil {
		return nil
	}

	return tasks
}

func GetAllUnFinishSyncTasks(limit int) []*Task {
	var tasks []*Task
	var err error
	// get all tasks progress is not 100%
	err = DB.Where("progress != ?", "100%").Where("status != ?", TaskStatusFailure).Where("status != ?", TaskStatusSuccess).Limit(limit).Order("id").Find(&tasks).Error
	if err != nil {
		return nil
	}
	return tasks
}

func GetByOnlyTaskId(taskId string) (*Task, bool, error) {
	if taskId == "" {
		return nil, false, nil
	}
	var task *Task
	var err error
	err = DB.Where("task_id = ?", taskId).First(&task).Error
	exist, err := RecordExist(err)
	if err != nil {
		return nil, false, err
	}
	return task, exist, err
}

func GetByTaskId(userId int, taskId string) (*Task, bool, error) {
	if taskId == "" {
		return nil, false, nil
	}
	var task *Task
	var err error
	err = DB.Where("user_id = ? and task_id = ?", userId, taskId).
		First(&task).Error
	exist, err := RecordExist(err)
	if err != nil {
		return nil, false, err
	}
	return task, exist, err
}

func GetByTaskIds(userId int, taskIds []any) ([]*Task, error) {
	if len(taskIds) == 0 {
		return nil, nil
	}
	var task []*Task
	var err error
	err = DB.Where("user_id = ? and task_id in (?)", userId, taskIds).
		Find(&task).Error
	if err != nil {
		return nil, err
	}
	return task, nil
}

func TaskUpdateProgress(id int64, progress string) error {
	return DB.Model(&Task{}).Where("id = ?", id).Update("progress", progress).Error
}

func (Task *Task) Insert() error {
	var err error
	err = DB.Create(Task).Error
	return err
}

func (Task *Task) Update() error {
	var err error
	err = DB.Save(Task).Error
	return err
}

func TaskBulkUpdate(TaskIds []string, params map[string]any) error {
	if len(TaskIds) == 0 {
		return nil
	}
	return DB.Model(&Task{}).
		Where("task_id in (?)", TaskIds).
		Updates(params).Error
}

func TaskBulkUpdateByTaskIds(taskIDs []int64, params map[string]any) error {
	if len(taskIDs) == 0 {
		return nil
	}
	return DB.Model(&Task{}).
		Where("id in (?)", taskIDs).
		Updates(params).Error
}

func TaskBulkUpdateByID(ids []int64, params map[string]any) error {
	if len(ids) == 0 {
		return nil
	}
	return DB.Model(&Task{}).
		Where("id in (?)", ids).
		Updates(params).Error
}

func SumUsedTaskQuota(queryParams SyncTaskQueryParams) (stat []TaskQuotaUsage, err error) {
	query := DB.Model(Task{})
	// 添加过滤条件
	if queryParams.ChannelID != "" {
		query = query.Where("channel_id = ?", queryParams.ChannelID)
	}
	if queryParams.UserID != "" {
		query = query.Where("user_id = ?", queryParams.UserID)
	}
	if len(queryParams.UserIDs) != 0 {
		query = query.Where("user_id in (?)", queryParams.UserIDs)
	}
	if queryParams.TaskID != "" {
		query = query.Where("task_id = ?", queryParams.TaskID)
	}
	if queryParams.Action != "" {
		query = query.Where("action = ?", queryParams.Action)
	}
	if queryParams.Status != "" {
		query = query.Where("status = ?", queryParams.Status)
	}
	if queryParams.StartTimestamp != 0 {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != 0 {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}
	err = query.Select("mode, sum(quota) as count").Group("mode").Find(&stat).Error
	return stat, err
}

// TaskCountAllTasks returns total tasks that match the given query params (admin usage)
func TaskCountAllTasks(queryParams SyncTaskQueryParams) int64 {
	var total int64
	query := DB.Model(&Task{})
	if queryParams.ChannelID != "" {
		query = query.Where("channel_id = ?", queryParams.ChannelID)
	}
	if queryParams.Platform != "" {
		query = query.Where("platform = ?", queryParams.Platform)
	}
	if queryParams.UserID != "" {
		query = query.Where("user_id = ?", queryParams.UserID)
	}
	if len(queryParams.UserIDs) != 0 {
		query = query.Where("user_id in (?)", queryParams.UserIDs)
	}
	if queryParams.TaskID != "" {
		query = query.Where("task_id = ?", queryParams.TaskID)
	}
	if queryParams.Action != "" {
		query = query.Where("action = ?", queryParams.Action)
	}
	if queryParams.Status != "" {
		query = query.Where("status = ?", queryParams.Status)
	}
	// ProjectID/RequestID must mirror the same filters TaskGetAllTasks
	// applies above (cycle-8 L10) — otherwise the admin list's reported
	// "total" would count unfiltered rows while the page itself only shows
	// the filtered ones.
	if queryParams.ProjectID != "" {
		query = query.Where("project_id = ?", queryParams.ProjectID)
	}
	if queryParams.RequestID != "" {
		query = query.Where("request_id = ?", queryParams.RequestID)
	}
	if queryParams.StartTimestamp != 0 {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != 0 {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}
	// TenantID/TenantScoped must mirror TaskGetAllTasks above, same
	// reasoning as the ProjectID/RequestID comment a few lines up —
	// otherwise the admin list's reported "total" would count every
	// tenant's rows (or, for a scoped-but-tenantless caller, every row too)
	// while the page itself only shows one tenant's or none.
	if queryParams.TenantScoped {
		query = query.Where("user_id IN (SELECT id FROM users WHERE tenant_id = ?)", queryParams.TenantID)
	}
	_ = query.Count(&total).Error
	return total
}

// TaskCountAllUserTask returns total tasks for given user
func TaskCountAllUserTask(userId int, queryParams SyncTaskQueryParams) int64 {
	var total int64
	query := DB.Model(&Task{}).Where("user_id = ?", userId)
	if queryParams.TaskID != "" {
		query = query.Where("task_id = ?", queryParams.TaskID)
	}
	if queryParams.Action != "" {
		query = query.Where("action = ?", queryParams.Action)
	}
	if queryParams.Status != "" {
		query = query.Where("status = ?", queryParams.Status)
	}
	if queryParams.Platform != "" {
		query = query.Where("platform = ?", queryParams.Platform)
	}
	// ProjectID/RequestID mirror TaskGetAllUserTask above; combined with the
	// user_id scope this query already carries, a project the caller does
	// not own yields a 0 total (fail-closed) rather than needing a separate
	// ownership check.
	if queryParams.ProjectID != "" {
		query = query.Where("project_id = ?", queryParams.ProjectID)
	}
	if queryParams.RequestID != "" {
		query = query.Where("request_id = ?", queryParams.RequestID)
	}
	if queryParams.StartTimestamp != 0 {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != 0 {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}
	_ = query.Count(&total).Error
	return total
}
func (t *Task) ToOpenAIVideo() *dto.OpenAIVideo {
	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = t.TaskID
	openAIVideo.Status = t.Status.ToVideoStatus()
	openAIVideo.Model = t.Properties.OriginModelName
	openAIVideo.SetProgressStr(t.Progress)
	openAIVideo.CreatedAt = t.CreatedAt
	openAIVideo.CompletedAt = t.UpdatedAt
	openAIVideo.SetMetadata("url", t.FailReason)
	return openAIVideo
}
