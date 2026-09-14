package entity

import (
	"database/sql/driver"
	"encoding/json"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

type TaskStatus string

func (t TaskStatus) ToVideoStatus() string {
	var status string
	switch t {
	case TaskStatusQueued, TaskStatusSubmitted:
		status = dto.VideoStatusQueued
	case TaskStatusInProgress:
		status = dto.VideoStatusInProgress
	case TaskStatusSuccess:
		status = dto.VideoStatusCompleted
	case TaskStatusFailure:
		status = dto.VideoStatusFailed
	default:
		status = dto.VideoStatusUnknown
	}
	return status
}

const (
	TaskStatusNotStart   TaskStatus = "NOT_START"
	TaskStatusSubmitted             = "SUBMITTED"
	TaskStatusQueued                = "QUEUED"
	TaskStatusInProgress            = "IN_PROGRESS"
	TaskStatusFailure               = "FAILURE"
	TaskStatusSuccess               = "SUCCESS"
	TaskStatusUnknown               = "UNKNOWN"
)

type Task struct {
	ID          int64                 `json:"id" gorm:"primary_key;AUTO_INCREMENT"`
	CreatedAt   int64                 `json:"created_at" gorm:"index"`
	UpdatedAt   int64                 `json:"updated_at"`
	TaskID      string                `json:"task_id" gorm:"type:varchar(191);index"`
	Platform    constant.TaskPlatform `json:"platform" gorm:"type:varchar(30);index"`
	UserId      int                   `json:"user_id" gorm:"index"`
	Group       string                `json:"group" gorm:"type:varchar(50)"`
	ChannelId   int                   `json:"channel_id" gorm:"index"`
	Quota       int                   `json:"quota"`
	Action      string                `json:"action" gorm:"type:varchar(40);index"`
	Status      TaskStatus            `json:"status" gorm:"type:varchar(20);index"`
	FailReason  string                `json:"fail_reason"`
	SubmitTime  int64                 `json:"submit_time" gorm:"index"`
	StartTime   int64                 `json:"start_time" gorm:"index"`
	FinishTime  int64                 `json:"finish_time" gorm:"index"`
	Progress    string                `json:"progress" gorm:"type:varchar(20);index"`
	Properties  Properties            `json:"properties" gorm:"type:json"`
	PrivateData TaskPrivateData       `json:"-" gorm:"column:private_data;type:json"`
	Data        json.RawMessage       `json:"data" gorm:"type:json"`
	// ProjectId attributes this task's spend to a Project (migration 035),
	// same cost-attribution convention as Token.ProjectId. The tag MUST stay
	// byte-identical to repo.Task.ProjectId (see the convention comment at
	// entity/token.go:52): both structs describe the same AutoMigrated
	// "tasks" table, so a divergence makes the two boots fight over the
	// column definition.
	ProjectId int `json:"project_id" gorm:"not null;default:0"`
	// RequestId is the gin request id (common.RequestIdKey) live when the
	// task was submitted — a support/cost-attribution lookup key, not an
	// upstream vendor id (that's TaskID above).
	RequestId string `json:"request_id" gorm:"type:varchar(64);not null;default:'';index"`
}

func (t *Task) SetData(data any) {
	b, _ := json.Marshal(data)
	t.Data = json.RawMessage(b)
}

func (t *Task) GetData(v any) error {
	return json.Unmarshal(t.Data, &v)
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

type Properties struct {
	Input             string `json:"input"`
	UpstreamModelName string `json:"upstream_model_name,omitempty"`
	OriginModelName   string `json:"origin_model_name,omitempty"`
}

func (m *Properties) Scan(val interface{}) error {
	bytesValue, _ := val.([]byte)
	if len(bytesValue) == 0 {
		*m = Properties{}
		return nil
	}
	return json.Unmarshal(bytesValue, m)
}

func (m Properties) Value() (driver.Value, error) {
	if m == (Properties{}) {
		return nil, nil
	}
	return json.Marshal(m)
}

type TaskPrivateData struct {
	Key string `json:"key,omitempty"`
}

func (p *TaskPrivateData) Scan(val interface{}) error {
	bytesValue, _ := val.([]byte)
	if len(bytesValue) == 0 {
		return nil
	}
	return json.Unmarshal(bytesValue, p)
}

func (p TaskPrivateData) Value() (driver.Value, error) {
	if (p == TaskPrivateData{}) {
		return nil, nil
	}
	return json.Marshal(p)
}

// SyncTaskQueryParams contains all search conditions for task queries
type SyncTaskQueryParams struct {
	Platform       constant.TaskPlatform
	ChannelID      string
	TaskID         string
	UserID         string
	Action         string
	Status         string
	StartTimestamp int64
	EndTimestamp   int64
	UserIDs        []int
	// ProjectID and RequestID filter admin/user task listings by the
	// cost-attribution columns migration 035 added (cycle-8 L8). L10 wires
	// these onto the list handlers; the query builders in repo/task.go
	// already apply them.
	ProjectID string
	RequestID string
}

type TaskQuotaUsage struct {
	Mode  string  `json:"mode"`
	Count float64 `json:"count"`
}
