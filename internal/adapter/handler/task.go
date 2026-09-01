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
	resp, err := adaptor.FetchTask(*channel.BaseURL, channel.Key, map[string]any{
		"ids": taskIds,
	}, proxy)
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

func GetAllTask(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)

	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	// 解析其他查询参数
	queryParams := repo.SyncTaskQueryParams{
		Platform:       constant.TaskPlatform(c.Query("platform")),
		TaskID:         c.Query("task_id"),
		Status:         c.Query("status"),
		Action:         c.Query("action"),
		StartTimestamp: startTimestamp,
		EndTimestamp:   endTimestamp,
		ChannelID:      c.Query("channel_id"),
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

	queryParams := repo.SyncTaskQueryParams{
		Platform:       constant.TaskPlatform(c.Query("platform")),
		TaskID:         c.Query("task_id"),
		Status:         c.Query("status"),
		Action:         c.Query("action"),
		StartTimestamp: startTimestamp,
		EndTimestamp:   endTimestamp,
	}

	items := repo.TaskGetAllUserTask(userId, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), queryParams)
	total := repo.TaskCountAllUserTask(userId, queryParams)
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}
