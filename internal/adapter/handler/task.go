package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/relay"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

func UpdateTaskBulk() {
	//revocer
	//imageModel := "midjourney"
	for {
		time.Sleep(time.Duration(15) * time.Second)
		common.SysLog("任务进度轮询开始")
		ctx := context.TODO()
		allTasks := repo.GetAllUnFinishSyncTasks(constant.TaskQueryLimit)
		platformTask := make(map[constant.TaskPlatform][]*repo.Task)
		for _, t := range allTasks {
			platformTask[t.Platform] = append(platformTask[t.Platform], t)
		}
		for platform, tasks := range platformTask {
			if len(tasks) == 0 {
				continue
			}
			taskChannelM := make(map[int][]string)
			taskM := make(map[string]*repo.Task)
			nullTaskIds := make([]int64, 0)
			for _, task := range tasks {
				if task.TaskID == "" {
					// 统计失败的未完成任务
					nullTaskIds = append(nullTaskIds, task.ID)
					continue
				}
				taskM[task.TaskID] = task
				taskChannelM[task.ChannelId] = append(taskChannelM[task.ChannelId], task.TaskID)
			}
			if len(nullTaskIds) > 0 {
				err := repo.TaskBulkUpdateByID(nullTaskIds, map[string]any{
					"status":   "FAILURE",
					"progress": "100%",
				})
				if err != nil {
					logger.LogError(ctx, fmt.Sprintf("Fix null task_id task error: %v", err))
				} else {
					logger.LogInfo(ctx, fmt.Sprintf("Fix null task_id task success: %v", nullTaskIds))
				}
			}
			if len(taskChannelM) == 0 {
				continue
			}

			UpdateTaskByPlatform(ctx, platform, taskChannelM, taskM)
		}
		common.SysLog("任务进度轮询完成")
	}
}

// UpdateTaskBulkWithContext updates tasks with context cancellation support.
func UpdateTaskBulkWithContext(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			common.SysLog("task bulk update stopped")
			return
		case <-ticker.C:
			// HA: only the leader polls task updates; non-leaders idle.
			if !common.IsLeader() {
				continue
			}
			common.SysLog("任务进度轮询开始")
			allTasks := repo.GetAllUnFinishSyncTasks(constant.TaskQueryLimit)
			platformTask := make(map[constant.TaskPlatform][]*repo.Task)
			for _, t := range allTasks {
				platformTask[t.Platform] = append(platformTask[t.Platform], t)
			}
			for platform, tasks := range platformTask {
				select {
				case <-ctx.Done():
					return
				default:
				}

				if len(tasks) == 0 {
					continue
				}
				taskChannelM := make(map[int][]string)
				taskM := make(map[string]*repo.Task)
				nullTaskIds := make([]int64, 0)
				for _, task := range tasks {
					if task.TaskID == "" {
						nullTaskIds = append(nullTaskIds, task.ID)
						continue
					}
					taskM[task.TaskID] = task
					taskChannelM[task.ChannelId] = append(taskChannelM[task.ChannelId], task.TaskID)
				}
				if len(nullTaskIds) > 0 {
					err := repo.TaskBulkUpdateByID(nullTaskIds, map[string]any{
						"status":   "FAILURE",
						"progress": "100%",
					})
					if err != nil {
						logger.LogError(ctx, fmt.Sprintf("Fix null task_id task error: %v", err))
					} else {
						logger.LogInfo(ctx, fmt.Sprintf("Fix null task_id task success: %v", nullTaskIds))
					}
				}
				if len(taskChannelM) == 0 {
					continue
				}

				UpdateTaskByPlatform(ctx, platform, taskChannelM, taskM)
			}
			common.SysLog("任务进度轮询完成")
		}
	}
}

func UpdateTaskByPlatform(ctx context.Context, platform constant.TaskPlatform, taskChannelM map[int][]string, taskM map[string]*repo.Task) {
	switch platform {
	case constant.TaskPlatformMidjourney:
		//_ = UpdateMidjourneyTaskAll(ctx, tasks)
	case constant.TaskPlatformSuno:
		_ = UpdateSunoTaskAll(ctx, taskChannelM, taskM)
	default:
		if err := UpdateVideoTaskAll(ctx, platform, taskChannelM, taskM); err != nil {
			common.SysLog(fmt.Sprintf("UpdateVideoTaskAll fail: %s", err))
		}
	}
}

func UpdateSunoTaskAll(ctx context.Context, taskChannelM map[int][]string, taskM map[string]*repo.Task) error {
	for channelId, taskIds := range taskChannelM {
		err := updateSunoTaskAll(ctx, channelId, taskIds, taskM)
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("渠道 #%d 更新异步任务失败: %s", channelId, err.Error()))
		}
	}
	return nil
}

func updateSunoTaskAll(ctx context.Context, channelId int, taskIds []string, taskM map[string]*repo.Task) error {
	logger.LogInfo(ctx, fmt.Sprintf("渠道 #%d 未完成的任务有: %d", channelId, len(taskIds)))
	if len(taskIds) == 0 {
		return nil
	}
	channel, err := repo.CacheGetChannel(channelId)
	if err != nil {
		common.SysLog(fmt.Sprintf("CacheGetChannel: %v", err))
		err = repo.TaskBulkUpdate(taskIds, map[string]any{
			"fail_reason": fmt.Sprintf("获取渠道信息失败，请联系管理员，渠道ID：%d", channelId),
			"status":      "FAILURE",
			"progress":    "100%",
		})
		if err != nil {
			common.SysLog(fmt.Sprintf("UpdateMidjourneyTask error2: %v", err))
		}
		return err
	}
	adaptor := relay.GetTaskAdaptor(constant.TaskPlatformSuno)
	if adaptor == nil {
		return errors.New("adaptor not found")
	}
	proxy := channel.GetSetting().Proxy
	forceHTTP1 := dto.ParamOverrideForceHTTP1(channel.GetParamOverride())
	resp, err := adaptor.FetchTask(*channel.BaseURL, channel.Key, map[string]any{
		"ids": taskIds,
	}, proxy, forceHTTP1)
	if err != nil {
		common.SysLog(fmt.Sprintf("Get Task Do req error: %v", err))
		return err
	}
	if resp.StatusCode != http.StatusOK {
		logger.LogError(ctx, fmt.Sprintf("Get Task status code: %d", resp.StatusCode))
		return errors.New(fmt.Sprintf("Get Task status code: %d", resp.StatusCode))
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		common.SysLog(fmt.Sprintf("Get Task parse body error: %v", err))
		return err
	}
	var responseItems dto.TaskResponse[[]dto.SunoDataResponse]
	err = json.Unmarshal(responseBody, &responseItems)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("Get Task parse body error2: %v, body: %s", err, string(responseBody)))
		return err
	}
	if !responseItems.IsSuccess() {
		common.SysLog(fmt.Sprintf("渠道 #%d 未完成的任务有: %d, 成功获取到任务数: %s", channelId, len(taskIds), string(responseBody)))
		return err
	}

	for _, responseItem := range responseItems.Data {
		task := taskM[responseItem.TaskID]
		if !checkTaskNeedUpdate(task, responseItem) {
			continue
		}

		task.Status = lo.If(repo.TaskStatus(responseItem.Status) != "", repo.TaskStatus(responseItem.Status)).Else(task.Status)
		task.FailReason = lo.If(responseItem.FailReason != "", responseItem.FailReason).Else(task.FailReason)
		task.SubmitTime = lo.If(responseItem.SubmitTime != 0, responseItem.SubmitTime).Else(task.SubmitTime)
		task.StartTime = lo.If(responseItem.StartTime != 0, responseItem.StartTime).Else(task.StartTime)
		task.FinishTime = lo.If(responseItem.FinishTime != 0, responseItem.FinishTime).Else(task.FinishTime)
		if responseItem.FailReason != "" || task.Status == repo.TaskStatusFailure {
			logger.LogInfo(ctx, task.TaskID+" 构建失败，"+task.FailReason)
			task.Progress = "100%"
			//err = repo.CacheUpdateUserQuota(task.UserId) ?
			if err != nil {
				logger.LogError(ctx, "error update user quota cache: "+err.Error())
			} else {
				quota := task.Quota
				refundTaskQuota(ctx, task.UserId, task.ChannelId, quota,
					fmt.Sprintf("异步任务执行失败 %s，补偿 %s", task.TaskID, logger.LogQuota(quota)))
			}
		}
		if responseItem.Status == repo.TaskStatusSuccess {
			task.Progress = "100%"
		}
		task.Data = responseItem.Data

		err = task.Update()
		if err != nil {
			common.SysLog("UpdateMidjourneyTask task error: " + err.Error())
		}
	}
	return nil
}

func checkTaskNeedUpdate(oldTask *repo.Task, newTask dto.SunoDataResponse) bool {

	if oldTask.SubmitTime != newTask.SubmitTime {
		return true
	}
	if oldTask.StartTime != newTask.StartTime {
		return true
	}
	if oldTask.FinishTime != newTask.FinishTime {
		return true
	}
	if string(oldTask.Status) != newTask.Status {
		return true
	}
	if oldTask.FailReason != newTask.FailReason {
		return true
	}
	if oldTask.FinishTime != newTask.FinishTime {
		return true
	}

	if (oldTask.Status == repo.TaskStatusFailure || oldTask.Status == repo.TaskStatusSuccess) && oldTask.Progress != "100%" {
		return true
	}

	oldData, _ := json.Marshal(oldTask.Data)
	newData, _ := json.Marshal(newTask.Data)

	sort.Slice(oldData, func(i, j int) bool {
		return oldData[i] < oldData[j]
	})
	sort.Slice(newData, func(i, j int) bool {
		return newData[i] < newData[j]
	})

	if string(oldData) != string(newData) {
		return true
	}
	return false
}

// normalizeRequestIDFilter trims whitespace off the request_id filter and
// reports it invalid when it exceeds 64 bytes (len(trimmed), not rune
// count) — tasks.request_id's own column width (migration 035).
// Byte-truncating an oversized value instead (the earlier version of this
// function) is wrong on two counts: a stored 64-byte request_id would then
// match a caller-supplied prefix of a longer string, and slicing at a
// fixed byte offset can split a multibyte UTF-8 rune, sending an invalid
// byte sequence into the query. Cycle-8 L10
// repair (ruling B-F2): callers must treat "invalid" as "cannot match any
// row" and skip the query entirely, not narrow it to a truncated value.
func normalizeRequestIDFilter(raw string) (value string, invalid bool) {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) > 64 {
		return "", true
	}
	return trimmed, false
}

// normalizeProjectIDFilter parses the project_id query param the way the v2
// log list does (GetLogsV2, v2_log.go): strconv.Atoi, filter only applied
// when the value is a positive integer. Unlike the log list — which folds a
// parse failure into projectID==0 (no filter) because repo.LogQueryParams.
// ProjectID is an int — the task query builders take ProjectID as a raw
// string forwarded straight into `project_id = ?` against an integer
// column (repo/task.go). Forwarding an unparseable string there raises a
// PostgreSQL 22P02 in production that the builder swallows into an empty
// result with the error discarded, not logged (TaskGetAllTasks returns nil
// on err, TaskCountAllTasks does `_ = query.Count(&total).Error`); parsing
// here first lets an invalid value short-circuit to the same empty page
// without hitting the database at all. Cycle-8 L10 repair (ruling B-F1).
func normalizeProjectIDFilter(raw string) (value string, invalid bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil || n <= 0 {
		return "", true
	}
	return strconv.Itoa(n), false
}

// respondEmptyTaskPage answers the fail-closed empty page for an invalid
// project_id/request_id filter without touching the database — SetItems
// gets an empty (not nil) slice so the response body is items:[] rather
// than items:null (cycle-8 L10 repair, ruling B-F1/B-F2).
func respondEmptyTaskPage(c *gin.Context, pageInfo *common.PageInfo) {
	pageInfo.SetTotal(0)
	pageInfo.SetItems([]*repo.Task{})
	common.ApiSuccess(c, pageInfo)
}

func GetAllTask(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)

	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)

	// ProjectID/RequestID: cost-attribution/support-lookup filters on the
	// migration-035 columns (cycle-8 L10); exact match. An invalid value for
	// either short-circuits to the empty page before any query builder or
	// DB call runs.
	projectID, projectIDInvalid := normalizeProjectIDFilter(c.Query("project_id"))
	requestID, requestIDInvalid := normalizeRequestIDFilter(c.Query("request_id"))
	if projectIDInvalid || requestIDInvalid {
		respondEmptyTaskPage(c, pageInfo)
		return
	}

	// 解析其他查询参数
	queryParams := repo.SyncTaskQueryParams{
		Platform:       constant.TaskPlatform(c.Query("platform")),
		TaskID:         c.Query("task_id"),
		Status:         c.Query("status"),
		Action:         c.Query("action"),
		StartTimestamp: startTimestamp,
		EndTimestamp:   endTimestamp,
		ChannelID:      c.Query("channel_id"),
		ProjectID:      projectID,
		RequestID:      requestID,
	}
	// This route is mounted under middleware.AdminAuth() (role >=
	// RoleAdminUser), not RootAuth — a tenant admin narrower than root must
	// not see other tenants' tasks. Mirrors GetAllChannels' isRoot/
	// callerTenant scope (channel.go:113-114,153-154): the filter is set
	// unconditionally for every non-root caller, including one whose
	// session carries no tenant yet, so an empty tenant closes the page
	// rather than falling through to the platform-wide view.
	if c.GetInt("role") < common.RoleRootUser {
		queryParams.TenantID = c.GetString("tenant_id")
		queryParams.TenantScoped = true
	}

	items := repo.TaskGetAllTasks(pageInfo.GetStartIdx(), pageInfo.GetPageSize(), queryParams)
	total := repo.TaskCountAllTasks(queryParams)
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

func GetUserTask(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)

	userId := c.GetInt("id")

	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)

	// ProjectID is combined with the userId scope both list functions
	// already apply below, so a project this user does not own yields an
	// empty page (fail-closed) rather than needing a separate ownership
	// check — mirrors GetLogsV2's ProjectID handling (v2_log.go), which
	// relies on the same UserID+ProjectID AND for the same reason. An
	// invalid (non-numeric / non-positive) value short-circuits the same
	// way, before any query builder or DB call runs.
	projectID, projectIDInvalid := normalizeProjectIDFilter(c.Query("project_id"))
	requestID, requestIDInvalid := normalizeRequestIDFilter(c.Query("request_id"))
	if projectIDInvalid || requestIDInvalid {
		respondEmptyTaskPage(c, pageInfo)
		return
	}

	queryParams := repo.SyncTaskQueryParams{
		Platform:       constant.TaskPlatform(c.Query("platform")),
		TaskID:         c.Query("task_id"),
		Status:         c.Query("status"),
		Action:         c.Query("action"),
		StartTimestamp: startTimestamp,
		EndTimestamp:   endTimestamp,
		ProjectID:      projectID,
		RequestID:      requestID,
	}

	items := repo.TaskGetAllUserTask(userId, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), queryParams)
	total := repo.TaskCountAllUserTask(userId, queryParams)
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}
