package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/relay"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
)

func UpdateVideoTaskAll(ctx context.Context, platform constant.TaskPlatform, taskChannelM map[int][]string, taskM map[string]*repo.Task) error {
	for channelId, taskIds := range taskChannelM {
		if err := updateVideoTaskAll(ctx, platform, channelId, taskIds, taskM); err != nil {
			logger.LogError(ctx, fmt.Sprintf("Channel #%d failed to update video async tasks: %s", channelId, err.Error()))
		}
	}
	return nil
}

func updateVideoTaskAll(ctx context.Context, platform constant.TaskPlatform, channelId int, taskIds []string, taskM map[string]*repo.Task) error {
	logger.LogInfo(ctx, fmt.Sprintf("Channel #%d pending video tasks: %d", channelId, len(taskIds)))
	if len(taskIds) == 0 {
		return nil
	}
	cacheGetChannel, err := repo.CacheGetChannel(channelId)
	if err != nil {
		errUpdate := repo.TaskBulkUpdate(taskIds, map[string]any{
			"fail_reason": fmt.Sprintf("Failed to get channel info, channel ID: %d", channelId),
			"status":      "FAILURE",
			"progress":    "100%",
		})
		if errUpdate != nil {
			common.SysLog(fmt.Sprintf("UpdateVideoTask error: %v", errUpdate))
		}
		return fmt.Errorf("CacheGetChannel failed: %w", err)
	}
	adaptor := relay.GetTaskAdaptor(platform)
	if adaptor == nil {
		return fmt.Errorf("video adaptor not found")
	}
	info := &relaycommon.RelayInfo{}
	info.ChannelMeta = &relaycommon.ChannelMeta{
		ChannelBaseUrl: cacheGetChannel.GetBaseURL(),
	}
	info.ApiKey = cacheGetChannel.Key
	adaptor.Init(info)
	for _, taskId := range taskIds {
		if err := updateVideoSingleTask(ctx, adaptor, cacheGetChannel, taskId, taskM); err != nil {
			logger.LogError(ctx, fmt.Sprintf("Failed to update video task %s: %s", taskId, err.Error()))
		}
	}
	return nil
}

func updateVideoSingleTask(ctx context.Context, adaptor provider.TaskAdaptor, channel *repo.Channel, taskId string, taskM map[string]*repo.Task) error {
	baseURL := constant.ChannelBaseURLs[channel.Type]
	if channel.GetBaseURL() != "" {
		baseURL = channel.GetBaseURL()
	}
	proxy := channel.GetSetting().Proxy

	task := taskM[taskId]
	if task == nil {
		logger.LogError(ctx, fmt.Sprintf("Task %s not found in taskM", taskId))
		return fmt.Errorf("task %s not found", taskId)
	}
	key := channel.Key

	privateData := task.PrivateData
	if privateData.Key != "" {
		key = privateData.Key
	}
	resp, err := adaptor.FetchTask(baseURL, key, map[string]any{
		"task_id": taskId,
		"action":  task.Action,
	}, proxy)
	if err != nil {
		return fmt.Errorf("fetchTask failed for task %s: %w", taskId, err)
	}
	//if resp.StatusCode != http.StatusOK {
	//return fmt.Errorf("get Video Task status code: %d", resp.StatusCode)
	//}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("readAll failed for task %s: %w", taskId, err)
	}

	logger.LogDebug(ctx, fmt.Sprintf("UpdateVideoSingleTask response: %s", string(responseBody)))

	taskResult := &relaycommon.TaskInfo{}
	// try parse as New API response format
	var responseItems dto.TaskResponse[repo.Task]
	if err = common.Unmarshal(responseBody, &responseItems); err == nil && responseItems.IsSuccess() {
		logger.LogDebug(ctx, fmt.Sprintf("UpdateVideoSingleTask parsed as new api response format: %+v", responseItems))
		t := responseItems.Data
		taskResult.TaskID = t.TaskID
		taskResult.Status = string(t.Status)
		taskResult.Url = t.FailReason
		taskResult.Progress = t.Progress
		taskResult.Reason = t.FailReason
		task.Data = t.Data
	} else if taskResult, err = adaptor.ParseTaskResult(responseBody); err != nil {
		return fmt.Errorf("parseTaskResult failed for task %s: %w", taskId, err)
	} else {
		task.Data = redactVideoResponseBody(responseBody)
	}

	logger.LogDebug(ctx, fmt.Sprintf("UpdateVideoSingleTask taskResult: %+v", taskResult))

	now := time.Now().Unix()
	if taskResult.Status == "" {
		//return fmt.Errorf("task %s status is empty", taskId)
		taskResult = relaycommon.FailTaskInfo("upstream returned empty status")
	}

	// 记录原本的状态，防止重复退款
	shouldRefund := false
	quota := task.Quota
	preStatus := task.Status

	task.Status = repo.TaskStatus(taskResult.Status)
	switch taskResult.Status {
	case repo.TaskStatusSubmitted:
		task.Progress = "10%"
	case repo.TaskStatusQueued:
		task.Progress = "20%"
	case repo.TaskStatusInProgress:
		task.Progress = "30%"
		if task.StartTime == 0 {
			task.StartTime = now
		}
	case repo.TaskStatusSuccess:
		task.Progress = "100%"
		if task.FinishTime == 0 {
			task.FinishTime = now
		}
		if !(len(taskResult.Url) > 5 && taskResult.Url[:5] == "data:") {
			task.FailReason = taskResult.Url
		}

		// 如果返回了 total_tokens 并且配置了模型倍率(非固定价格),则重新计费
		if taskResult.TotalTokens > 0 {
			// 获取模型名称
			var taskData map[string]interface{}
			if err := json.Unmarshal(task.Data, &taskData); err == nil {
				if modelName, ok := taskData["model"].(string); ok && modelName != "" {
					// 获取模型价格和倍率
					modelRatio, hasRatioSetting, _ := ratio_setting.GetModelRatio(modelName)
					// 只有配置了倍率(非固定价格)时才按 token 重新计费
					if hasRatioSetting && modelRatio > 0 {
						// 获取用户和组的倍率信息
						group := task.Group
						if group == "" {
							user, err := repo.GetUserById(task.UserId, false)
							if err == nil {
								group = user.Group
							}
						}
						if group != "" {
							groupRatio := ratio_setting.GetGroupRatio(group)
							userGroupRatio, hasUserGroupRatio := ratio_setting.GetGroupGroupRatio(group, group)

							var finalGroupRatio float64
							if hasUserGroupRatio {
								finalGroupRatio = userGroupRatio
							} else {
								finalGroupRatio = groupRatio
							}

							// 计算实际应扣费额度: totalTokens * modelRatio * groupRatio
							actualQuota := int(float64(taskResult.TotalTokens) * modelRatio * finalGroupRatio)

							// 计算差额
							preConsumedQuota := task.Quota
							quotaDelta := actualQuota - preConsumedQuota

							if quotaDelta > 0 {
								// 需要补扣费
								logger.LogInfo(ctx, fmt.Sprintf("视频任务 %s 预扣费后补扣费：%s（实际消耗：%s，预扣费：%s，tokens：%d）",
									task.TaskID,
									logger.LogQuota(quotaDelta),
									logger.LogQuota(actualQuota),
									logger.LogQuota(preConsumedQuota),
									taskResult.TotalTokens,
								))
								if err := repo.DecreaseUserQuota(task.UserId, quotaDelta); err != nil {
									logger.LogError(ctx, fmt.Sprintf("补扣费失败: %s", err.Error()))
								} else {
									repo.UpdateUserUsedQuotaAndRequestCount(task.UserId, quotaDelta)
									repo.UpdateChannelUsedQuota(task.ChannelId, quotaDelta)
									task.Quota = actualQuota // 更新任务记录的实际扣费额度

									// 记录消费日志
									logContent := fmt.Sprintf("视频任务成功补扣费，模型倍率 %.2f，分组倍率 %.2f，tokens %d，预扣费 %s，实际扣费 %s，补扣费 %s",
										modelRatio, finalGroupRatio, taskResult.TotalTokens,
										logger.LogQuota(preConsumedQuota), logger.LogQuota(actualQuota), logger.LogQuota(quotaDelta))
									repo.RecordLog(task.UserId, repo.LogTypeSystem, logContent)
								}
							} else if quotaDelta < 0 {
								// 需要退还多扣的费用
								refundQuota := -quotaDelta
								logger.LogInfo(ctx, fmt.Sprintf("视频任务 %s 预扣费后返还：%s（实际消耗：%s，预扣费：%s，tokens：%d）",
									task.TaskID,
									logger.LogQuota(refundQuota),
									logger.LogQuota(actualQuota),
									logger.LogQuota(preConsumedQuota),
									taskResult.TotalTokens,
								))
								// Mirror of the quotaDelta > 0 branch above, which moves BOTH
								// used_quota counters when it charges the difference. Giving
								// money back has to move the same two the other way, or an
								// over-estimated pre-consume leaves used_quota showing the
								// estimate forever — that asymmetry lived inside this single
								// if/else until 2026-09-01.
								refundLog := fmt.Sprintf("视频任务成功退还多扣费用，模型倍率 %.2f，分组倍率 %.2f，tokens %d，预扣费 %s，实际扣费 %s，退还 %s",
									modelRatio, finalGroupRatio, taskResult.TotalTokens,
									logger.LogQuota(preConsumedQuota), logger.LogQuota(actualQuota), logger.LogQuota(refundQuota))
								if refundTaskQuota(ctx, task.UserId, task.ChannelId, refundQuota, refundLog) {
									task.Quota = actualQuota // 更新任务记录的实际扣费额度
								}
							} else {
								// quotaDelta == 0, 预扣费刚好准确
								logger.LogInfo(ctx, fmt.Sprintf("视频任务 %s 预扣费准确（%s，tokens：%d）",
									task.TaskID, logger.LogQuota(actualQuota), taskResult.TotalTokens))
							}
						}
					}
				}
			}
		}
	case repo.TaskStatusFailure:
		logger.LogJson(ctx, fmt.Sprintf("Task %s failed", taskId), task)
		task.Status = repo.TaskStatusFailure
		task.Progress = "100%"
		if task.FinishTime == 0 {
			task.FinishTime = now
		}
		task.FailReason = taskResult.Reason
		logger.LogInfo(ctx, fmt.Sprintf("Task %s failed: %s", task.TaskID, task.FailReason))
		taskResult.Progress = "100%"
		if quota != 0 {
			if preStatus != repo.TaskStatusFailure {
				shouldRefund = true
			} else {
				logger.LogWarn(ctx, fmt.Sprintf("Task %s already in failure status, skip refund", task.TaskID))
			}
		}
	default:
		return fmt.Errorf("unknown task status %s for task %s", taskResult.Status, taskId)
	}
	if taskResult.Progress != "" {
		task.Progress = taskResult.Progress
	}
	if err := task.Update(); err != nil {
		common.SysLog("UpdateVideoTask task error: " + err.Error())
		shouldRefund = false
	}

	if shouldRefund {
		// 任务失败且之前状态不是失败才退还额度，防止重复退还
		refundTaskQuota(ctx, task.UserId, task.ChannelId, quota,
			fmt.Sprintf("Video async task failed %s, refund %s", task.TaskID, logger.LogQuota(quota)))
	}

	return nil
}

func redactVideoResponseBody(body []byte) []byte {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	resp, _ := m["response"].(map[string]any)
	if resp != nil {
		delete(resp, "bytesBase64Encoded")
		if v, ok := resp["video"].(string); ok {
			resp["video"] = truncateBase64(v)
		}
		if vs, ok := resp["videos"].([]any); ok {
			for i := range vs {
				if vm, ok := vs[i].(map[string]any); ok {
					delete(vm, "bytesBase64Encoded")
				}
			}
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return b
}

func truncateBase64(s string) string {
	const maxKeep = 256
	if len(s) <= maxKeep {
		return s
	}
	return s[:maxKeep] + "..."
}
