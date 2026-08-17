package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

/*
Task 任务通过平台、Action 区分任务
*/
func RelayTaskSubmit(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *dto.TaskError) {
	info.InitChannelMeta(c)
	// ensure TaskRelayInfo is initialized to avoid nil dereference when accessing embedded fields
	if info.TaskRelayInfo == nil {
		info.TaskRelayInfo = &relaycommon.TaskRelayInfo{}
	}
	path := c.Request.URL.Path
	if strings.Contains(path, "/v1/videos/") && strings.HasSuffix(path, "/remix") {
		info.Action = constant.TaskActionRemix
	}

	// 提取 remix 任务的 video_id
	if info.Action == constant.TaskActionRemix {
		videoID := c.Param("video_id")
		if strings.TrimSpace(videoID) == "" {
			return app.TaskErrorWrapperLocal(fmt.Errorf("video_id is required"), "invalid_request", http.StatusBadRequest)
		}
		info.OriginTaskID = videoID
	}

	platform := constant.TaskPlatform(c.GetString("platform"))

	// 获取原始任务信息
	if info.OriginTaskID != "" {
		originTask, exist, err := repo.GetByTaskId(info.UserId, info.OriginTaskID)
		if err != nil {
			taskErr = app.TaskErrorWrapper(err, "get_origin_task_failed", http.StatusInternalServerError)
			return
		}
		if !exist {
			taskErr = app.TaskErrorWrapperLocal(errors.New("task_origin_not_exist"), "task_not_exist", http.StatusBadRequest)
			return
		}
		if info.OriginModelName == "" {
			if originTask.Properties.OriginModelName != "" {
				info.OriginModelName = originTask.Properties.OriginModelName
			} else if originTask.Properties.UpstreamModelName != "" {
				info.OriginModelName = originTask.Properties.UpstreamModelName
			} else {
				var taskData map[string]interface{}
				_ = json.Unmarshal(originTask.Data, &taskData)
				if m, ok := taskData["model"].(string); ok && m != "" {
					info.OriginModelName = m
					platform = originTask.Platform
				}
			}
		}
		if originTask.ChannelId != info.ChannelId {
			channel, err := repo.GetChannelById(originTask.ChannelId, true)
			if err != nil {
				taskErr = app.TaskErrorWrapperLocal(err, "channel_not_found", http.StatusBadRequest)
				return
			}
			if channel.Status != common.ChannelStatusEnabled {
				taskErr = app.TaskErrorWrapperLocal(errors.New("the channel of the origin task is disabled"), "task_channel_disable", http.StatusBadRequest)
				return
			}
			key, _, newAPIError := channel.GetNextEnabledKey()
			if newAPIError != nil {
				taskErr = app.TaskErrorWrapper(newAPIError, "channel_no_available_key", newAPIError.StatusCode)
				return
			}
			common.SetContextKey(c, constant.ContextKeyChannelKey, key)
			common.SetContextKey(c, constant.ContextKeyChannelType, channel.Type)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, channel.GetBaseURL())
			common.SetContextKey(c, constant.ContextKeyChannelId, originTask.ChannelId)

			info.ChannelBaseUrl = channel.GetBaseURL()
			info.ChannelId = originTask.ChannelId
			info.ChannelType = channel.Type
			info.ApiKey = key
			platform = originTask.Platform
		}

		// 使用原始任务的参数
		if info.Action == constant.TaskActionRemix {
			var taskData map[string]interface{}
			_ = json.Unmarshal(originTask.Data, &taskData)
			secondsStr, _ := taskData["seconds"].(string)
			seconds, _ := strconv.Atoi(secondsStr)
			if seconds <= 0 {
				seconds = 4
			}
			sizeStr, _ := taskData["size"].(string)
			if info.PriceData.OtherRatios == nil {
				info.PriceData.OtherRatios = map[string]float64{}
			}
			info.PriceData.OtherRatios["seconds"] = float64(seconds)
			info.PriceData.OtherRatios["size"] = 1
			if sizeStr == "1792x1024" || sizeStr == "1024x1792" {
				info.PriceData.OtherRatios["size"] = 1.666667
			}
		}
	}
	if platform == "" {
		platform = GetTaskPlatform(c)
	}

	info.InitChannelMeta(c)
	adaptor := GetTaskAdaptor(platform)
	if adaptor == nil {
		return app.TaskErrorWrapperLocal(fmt.Errorf("invalid api platform: %s", platform), "invalid_api_platform", http.StatusBadRequest)
	}
	adaptor.Init(info)
	// get & validate taskRequest 获取并验证文本请求
	taskErr = adaptor.ValidateRequestAndSetAction(c, info)
	if taskErr != nil {
		return
	}

	modelName := info.OriginModelName
	if modelName == "" {
		modelName = app.CoverTaskActionToModelName(platform, info.Action)
	}
	modelPrice, success := ratio_setting.GetModelPrice(modelName, true)
	if !success {
		defaultPrice, ok := ratio_setting.GetDefaultModelPriceMap()[modelName]
		if !ok {
			modelPrice = 0.1
		} else {
			modelPrice = defaultPrice
		}
	}

	// 预扣
	groupRatio := ratio_setting.GetGroupRatio(info.UsingGroup)
	var ratio float64
	userGroupRatio, hasUserGroupRatio := ratio_setting.GetGroupGroupRatio(info.UserGroup, info.UsingGroup)
	if hasUserGroupRatio {
		ratio = modelPrice * userGroupRatio
	} else {
		ratio = modelPrice * groupRatio
	}
	// FIXME: 临时修补，支持任务仅按次计费
	if !common.StringsContains(constant.TaskPricePatches, modelName) {
		if len(info.PriceData.OtherRatios) > 0 {
			for _, ra := range info.PriceData.OtherRatios {
				if 1.0 != ra {
					ratio *= ra
				}
			}
		}
	}
	println(fmt.Sprintf("model: %s, model_price: %.4f, group: %s, group_ratio: %.4f, final_ratio: %.4f", modelName, modelPrice, info.UsingGroup, groupRatio, ratio))
	userQuota, err := repo.GetUserQuota(info.UserId, false)
	if err != nil {
		taskErr = app.TaskErrorWrapper(err, "get_user_quota_failed", http.StatusInternalServerError)
		return
	}
	quota := int(ratio * common.QuotaPerUnit)
	if userQuota-quota < 0 {
		taskErr = app.TaskErrorWrapperLocal(errors.New("user quota is not enough"), "quota_not_enough", http.StatusForbidden)
		return
	}

	// build body
	requestBody, err := adaptor.BuildRequestBody(c, info)
	if err != nil {
		taskErr = app.TaskErrorWrapper(err, "build_request_failed", http.StatusInternalServerError)
		return
	}
	// do request
	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		taskErr = app.TaskErrorWrapper(err, "do_request_failed", http.StatusInternalServerError)
		return
	}
	// handle response
	if resp != nil && resp.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(resp.Body)
		taskErr = app.TaskErrorWrapper(fmt.Errorf("%s", string(responseBody)), "fail_to_fetch_task", resp.StatusCode)
		return
	}

	defer func() {
		// release quota
		if info.ConsumeQuota && taskErr == nil {

			err := app.PostConsumeQuota(info, quota, 0, true)
			if err != nil {
				common.SysLog("error consuming token remain quota: " + err.Error())
			}
			if quota != 0 {
				tokenName := c.GetString("token_name")
				//gRatio := groupRatio
				//if hasUserGroupRatio {
				//	gRatio = userGroupRatio
				//}
				logContent := fmt.Sprintf("操作 %s", info.Action)
				// FIXME: 临时修补，支持任务仅按次计费
				if common.StringsContains(constant.TaskPricePatches, modelName) {
					logContent = fmt.Sprintf("%s，按次计费", logContent)
				} else {
					if len(info.PriceData.OtherRatios) > 0 {
						var contents []string
						for key, ra := range info.PriceData.OtherRatios {
							if 1.0 != ra {
								contents = append(contents, fmt.Sprintf("%s: %.2f", key, ra))
							}
						}
						if len(contents) > 0 {
							logContent = fmt.Sprintf("%s, 计算参数：%s", logContent, strings.Join(contents, ", "))
						}
					}
				}
				other := make(map[string]interface{})
				if c != nil && c.Request != nil && c.Request.URL != nil {
					other["request_path"] = c.Request.URL.Path
				}
				other["model_price"] = modelPrice
				other["group_ratio"] = groupRatio
				if hasUserGroupRatio {
					other["user_group_ratio"] = userGroupRatio
				}
				logParams := repo.RecordConsumeLogParams{
					ChannelId: info.ChannelId,
					ModelName: modelName,
					TokenName: tokenName,
					Quota:     quota,
					Content:   logContent,
					TokenId:   info.TokenId,
					Group:     info.UsingGroup,
					Other:     other,
				}
				governance.EnrichLogParams(c, info, &logParams)
				repo.RecordConsumeLog(c, info.UserId, logParams)
				repo.UpdateUserUsedQuotaAndRequestCount(info.UserId, quota)
				repo.UpdateChannelUsedQuota(info.ChannelId, quota)
			}
		}
	}()

	taskID, taskData, taskErr := adaptor.DoResponse(c, resp, info)
	if taskErr != nil {
		return
	}
	info.ConsumeQuota = true
	// insert task
	task := repo.InitTask(platform, info)
	task.TaskID = taskID
	task.Quota = quota
	task.Data = taskData
	task.Action = info.Action
	err = task.Insert()
	if err != nil {
		taskErr = app.TaskErrorWrapper(err, "insert_task_failed", http.StatusInternalServerError)
		return
	}
	return nil
}

var fetchRespBuilders = map[int]func(c *gin.Context) (respBody []byte, taskResp *dto.TaskError){
	relayconstant.RelayModeSunoFetchByID:  sunoFetchByIDRespBodyBuilder,
	relayconstant.RelayModeSunoFetch:      sunoFetchRespBodyBuilder,
	relayconstant.RelayModeVideoFetchByID: videoFetchByIDRespBodyBuilder,
	relayconstant.RelayModeMusicFetchByID: musicFetchByIDRespBodyBuilder,
}

func RelayTaskFetch(c *gin.Context, relayMode int) (taskResp *dto.TaskError) {
	respBuilder, ok := fetchRespBuilders[relayMode]
	if !ok {
		taskResp = app.TaskErrorWrapperLocal(errors.New("invalid_relay_mode"), "invalid_relay_mode", http.StatusBadRequest)
	}

	respBody, taskErr := respBuilder(c)
	if taskErr != nil {
		return taskErr
	}
	if len(respBody) == 0 {
		respBody = []byte("{\"code\":\"success\",\"data\":null}")
	}

	c.Writer.Header().Set("Content-Type", "application/json")
	_, err := io.Copy(c.Writer, bytes.NewBuffer(respBody))
	if err != nil {
		taskResp = app.TaskErrorWrapper(err, "copy_response_body_failed", http.StatusInternalServerError)
		return
	}
	return
}

func sunoFetchRespBodyBuilder(c *gin.Context) (respBody []byte, taskResp *dto.TaskError) {
	userId := c.GetInt("id")
	var condition = struct {
		IDs    []any  `json:"ids"`
		Action string `json:"action"`
	}{}
	err := c.BindJSON(&condition)
	if err != nil {
		taskResp = app.TaskErrorWrapper(err, "invalid_request", http.StatusBadRequest)
		return
	}
	var tasks []any
	if len(condition.IDs) > 0 {
		taskModels, err := repo.GetByTaskIds(userId, condition.IDs)
		if err != nil {
			taskResp = app.TaskErrorWrapper(err, "get_tasks_failed", http.StatusInternalServerError)
			return
		}
		for _, task := range taskModels {
			tasks = append(tasks, TaskModel2Dto(task))
		}
	} else {
		tasks = make([]any, 0)
	}
	respBody, err = json.Marshal(dto.TaskResponse[[]any]{
		Code: "success",
		Data: tasks,
	})
	// Dropping this error returned (nil, nil), which RelayTaskFetch then serves
	// as a 200 with a literal `{"code":"success","data":null}` — a query failure
	// disguised as an empty result. Reported the same way as the video and music
	// builders below.
	//
	// Known consequence: 500 puts this on the retry path
	// (handler.shouldRetryTaskRelay treats any 5xx as retryable, and it tests
	// StatusCode before LocalError), so a row whose Data is unmarshalable will
	// retry up to RetryTimes on a failure that is local and deterministic. That
	// is wasted work, not a correctness bug, and it is the behaviour the video
	// and music builders already had — narrowing it means reordering the retry
	// policy, which is a separate change.
	if err != nil {
		taskResp = app.TaskErrorWrapper(err, "marshal_response_failed", http.StatusInternalServerError)
	}
	return
}

func sunoFetchByIDRespBodyBuilder(c *gin.Context) (respBody []byte, taskResp *dto.TaskError) {
	taskId := c.Param("id")
	userId := c.GetInt("id")

	originTask, exist, err := repo.GetByTaskId(userId, taskId)
	if err != nil {
		taskResp = app.TaskErrorWrapper(err, "get_task_failed", http.StatusInternalServerError)
		return
	}
	if !exist {
		taskResp = app.TaskErrorWrapperLocal(errors.New("task_not_exist"), "task_not_exist", http.StatusBadRequest)
		return
	}

	respBody, err = json.Marshal(dto.TaskResponse[any]{
		Code: "success",
		Data: TaskModel2Dto(originTask),
	})
	if err != nil {
		taskResp = app.TaskErrorWrapper(err, "marshal_response_failed", http.StatusInternalServerError)
	}
	return
}

func videoFetchByIDRespBodyBuilder(c *gin.Context) (respBody []byte, taskResp *dto.TaskError) {
	taskId := c.Param("task_id")
	if taskId == "" {
		taskId = c.GetString("task_id")
	}
	userId := c.GetInt("id")

	originTask, exist, err := repo.GetByTaskId(userId, taskId)
	if err != nil {
		taskResp = app.TaskErrorWrapper(err, "get_task_failed", http.StatusInternalServerError)
		return
	}
	if !exist {
		taskResp = app.TaskErrorWrapperLocal(errors.New("task_not_exist"), "task_not_exist", http.StatusBadRequest)
		return
	}

	func() {
		channelModel, err2 := repo.GetChannelById(originTask.ChannelId, true)
		if err2 != nil {
			return
		}
		if channelModel.Type != constant.ChannelTypeVertexAi && channelModel.Type != constant.ChannelTypeGemini {
			return
		}
		baseURL := constant.ChannelBaseURLs[channelModel.Type]
		if channelModel.GetBaseURL() != "" {
			baseURL = channelModel.GetBaseURL()
		}
		proxy := channelModel.GetSetting().Proxy
		adaptor := GetTaskAdaptor(constant.TaskPlatform(strconv.Itoa(channelModel.Type)))
		if adaptor == nil {
			return
		}
		resp, err2 := adaptor.FetchTask(baseURL, channelModel.Key, map[string]any{
			"task_id": originTask.TaskID,
			"action":  originTask.Action,
		}, proxy)
		if err2 != nil || resp == nil {
			return
		}
		defer resp.Body.Close()
		body, err2 := io.ReadAll(resp.Body)
		if err2 != nil {
			return
		}
		ti, err2 := adaptor.ParseTaskResult(body)
		if err2 == nil && ti != nil {
			if ti.Status != "" {
				originTask.Status = repo.TaskStatus(ti.Status)
			}
			if ti.Progress != "" {
				originTask.Progress = ti.Progress
			}
			if ti.Url != "" {
				if strings.HasPrefix(ti.Url, "data:") {
				} else {
					originTask.FailReason = ti.Url
				}
			}
			_ = originTask.Update()
			var raw map[string]any
			_ = json.Unmarshal(body, &raw)
			format := "mp4"
			if respObj, ok := raw["response"].(map[string]any); ok {
				if vids, ok := respObj["videos"].([]any); ok && len(vids) > 0 {
					if v0, ok := vids[0].(map[string]any); ok {
						if mt, ok := v0["mimeType"].(string); ok && mt != "" {
							if strings.Contains(mt, "mp4") {
								format = "mp4"
							} else {
								format = mt
							}
						}
					}
				}
			}
			status := "processing"
			switch originTask.Status {
			case repo.TaskStatusSuccess:
				status = "succeeded"
			case repo.TaskStatusFailure:
				status = "failed"
			case repo.TaskStatusQueued, repo.TaskStatusSubmitted:
				status = "queued"
			}
			if !strings.HasPrefix(c.Request.RequestURI, "/v1/videos/") {
				out := map[string]any{
					"error":    nil,
					"format":   format,
					"metadata": nil,
					"status":   status,
					"task_id":  originTask.TaskID,
					"url":      originTask.FailReason,
				}
				respBody, _ = json.Marshal(dto.TaskResponse[any]{
					Code: "success",
					Data: out,
				})
			}
		}
	}()

	if len(respBody) != 0 {
		return
	}

	if strings.HasPrefix(c.Request.RequestURI, "/v1/videos/") {
		adaptor := GetTaskAdaptor(originTask.Platform)
		if adaptor == nil {
			taskResp = app.TaskErrorWrapperLocal(fmt.Errorf("invalid channel id: %d", originTask.ChannelId), "invalid_channel_id", http.StatusBadRequest)
			return
		}
		if converter, ok := adaptor.(provider.OpenAIVideoConverter); ok {
			openAIVideoData, err := converter.ConvertToOpenAIVideo(originTask)
			if err != nil {
				taskResp = app.TaskErrorWrapper(err, "convert_to_openai_video_failed", http.StatusInternalServerError)
				return
			}
			respBody = openAIVideoData
			return
		}
		taskResp = app.TaskErrorWrapperLocal(errors.New(fmt.Sprintf("not_implemented:%s", originTask.Platform)), "not_implemented", http.StatusNotImplemented)
		return
	}
	respBody, err = json.Marshal(dto.TaskResponse[any]{
		Code: "success",
		Data: TaskModel2Dto(originTask),
	})
	if err != nil {
		taskResp = app.TaskErrorWrapper(err, "marshal_response_failed", http.StatusInternalServerError)
	}
	return
}

func musicFetchByIDRespBodyBuilder(c *gin.Context) (respBody []byte, taskResp *dto.TaskError) {
	taskId := c.Param("task_id")
	userId := c.GetInt("id")

	originTask, exist, err := repo.GetByTaskId(userId, taskId)
	if err != nil {
		taskResp = app.TaskErrorWrapper(err, "get_task_failed", http.StatusInternalServerError)
		return
	}
	if !exist {
		taskResp = app.TaskErrorWrapperLocal(errors.New("task_not_exist"), "task_not_exist", http.StatusNotFound)
		return
	}

	// Map internal status to standardized music response status.
	status := "in_progress"
	progress := 50
	switch originTask.Status {
	case repo.TaskStatusSuccess:
		status = "completed"
		progress = 100
	case repo.TaskStatusFailure:
		status = "failed"
		progress = 0
	case repo.TaskStatusQueued, repo.TaskStatusSubmitted, repo.TaskStatusNotStart:
		status = "queued"
		progress = 0
	case repo.TaskStatusInProgress:
		status = "in_progress"
		progress = 50
	}

	// Parse progress from task if available.
	if originTask.Progress != "" {
		var pct int
		if _, err := fmt.Sscanf(originTask.Progress, "%d%%", &pct); err == nil {
			progress = pct
		}
	}

	resp := &dto.MusicFetchResponse{
		ID:       originTask.TaskID,
		Status:   status,
		Progress: progress,
	}

	// Extract audio URL and title from Suno data if available.
	if len(originTask.Data) > 0 {
		var data map[string]any
		if json.Unmarshal(originTask.Data, &data) == nil {
			extractMusicDataFromSunoResponse(resp, data)
		}
	}

	// FailReason may contain the audio URL (set by ParseTaskResult).
	if resp.AudioURL == "" && originTask.FailReason != "" && status == "completed" {
		resp.AudioURL = originTask.FailReason
	}

	if status == "failed" {
		msg := originTask.FailReason
		if msg == "" {
			msg = "music generation failed"
		}
		resp.Error = &struct {
			Message string `json:"message"`
		}{Message: msg}
	}

	respBody, err = json.Marshal(resp)
	if err != nil {
		taskResp = app.TaskErrorWrapper(err, "marshal_response_failed", http.StatusInternalServerError)
	}
	return
}

// extractMusicDataFromSunoResponse tries to pull audio_url, title, duration
// from the nested Suno task data structure.
func extractMusicDataFromSunoResponse(resp *dto.MusicFetchResponse, data map[string]any) {
	// Suno response wraps song data in "data" field, which may be a map or array.
	innerData, _ := data["data"].(map[string]any)
	if innerData == nil {
		// Try array of clips.
		if clips, ok := data["clips"].(map[string]any); ok {
			for _, v := range clips {
				clip, ok := v.(map[string]any)
				if !ok {
					continue
				}
				if audioURL, ok := clip["audio_url"].(string); ok && audioURL != "" {
					resp.AudioURL = audioURL
				}
				if title, ok := clip["title"].(string); ok {
					resp.Title = title
				}
				if md, ok := clip["metadata"].(map[string]any); ok {
					if dur, ok := md["duration"].(float64); ok {
						resp.Duration = dur
					}
				}
				break // Take the first clip.
			}
		}
		return
	}
	if audioURL, ok := innerData["audio_url"].(string); ok && audioURL != "" {
		resp.AudioURL = audioURL
	}
	if title, ok := innerData["title"].(string); ok {
		resp.Title = title
	}
}

func TaskModel2Dto(task *repo.Task) *dto.TaskDto {
	return &dto.TaskDto{
		TaskID:     task.TaskID,
		Action:     task.Action,
		Status:     string(task.Status),
		FailReason: task.FailReason,
		SubmitTime: task.SubmitTime,
		StartTime:  task.StartTime,
		FinishTime: task.FinishTime,
		Progress:   task.Progress,
		Data:       task.Data,
	}
}
