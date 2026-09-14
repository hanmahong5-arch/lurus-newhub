package handler

// task_generic.go — the generic async-task surface (cycle-8 L8,
// tasks-plugins-01/18): POST /v1/tasks/:platform (submit) and
// GET /v1/tasks/:platform/:task_id (status), mounted by
// router.SetTaskRouter on the same TokenAuth/PoolBalanceCheck/
// CostSpikeLimit/EntitlementCheck/ModelRequestRateLimit/BusinessRateLimit/
// RelayConcurrencyLimit chain as the dedicated /suno and /v1/audio/music
// groups (relay-router.go). Submit dispatches through the existing
// RelayTask machinery unchanged — TaskPlatformGuard below only sets the
// same "platform" (and, for suno, "action") context keys the dedicated
// routes already set ahead of channel selection — so billing, InitTask and
// the poller (task.go's UpdateTaskBulkWithContext) are untouched. Status
// does NOT go through RelayTask: the poller is what keeps a task row's
// status current, so this is a plain ownership-checked read of the tasks
// table, the same shape as video_proxy.go's ownership check but answering
// 404 (not 403) so the route can't be used to enumerate task ids that
// belong to someone else.
//
// This does not add a new task platform or adaptor: :platform is validated
// against genericTaskPlatforms below, one entry per compiled adaptor under
// internal/adapter/provider/task/*, each checked against relay.GetTaskAdaptor
// (the same resolver RelayTaskSubmit itself uses, not a new plugin
// registry) by TestGenericTaskPlatforms_CoverCompiledAdaptors.

import (
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/relay"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// genericTaskPlatforms maps the /v1/tasks/:platform URL segment to the
// constant.TaskPlatform key relay.GetTaskAdaptor resolves. Suno and Music
// are matched by their own literal string key — exactly what the dedicated
// /suno and /v1/audio/music route groups set on "platform" in
// distributor.go's getModelRequest, ahead of any channel selection,
// because neither has a distinct numeric ChannelType GetTaskAdaptor's
// fallback switch recognises (both, in fact, only ever run against a
// ChannelTypeSunoAPI channel — music.TaskAdaptor translates its request
// into Suno's own wire format, see genericTaskChannelTypes below). The
// other nine resolve by the selected channel's numeric ChannelType
// (mirrored here as its decimal string).
var genericTaskPlatforms = map[string]constant.TaskPlatform{
	"suno":   constant.TaskPlatformSuno,
	"music":  constant.TaskPlatformMusic,
	"ali":    constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeAli)),
	"kling":  constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeKling)),
	"jimeng": constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeJimeng)),
	"vertex": constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeVertexAi)),
	"vidu":   constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeVidu)),
	"doubao": constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeDoubaoVideo)),
	"sora":   constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeSora)),
	"gemini": constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeGemini)),
	"hailuo": constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeMiniMax)),
}

// genericTaskChannelTypes is the allow-list TaskChannelPlatformGuard checks
// the POST leg's Distribute()-selected channel_type against. It exists
// because RelayTaskSubmit (relay_task.go) reads the "platform" context key
// set by TaskPlatformGuard BEFORE ever falling back to the selected
// channel's channel_type, so without this guard a declared :platform is
// dispatched against whatever channel the request body's "model" happens to
// resolve to via Distribute — e.g. POST /v1/tasks/kling with a model that
// resolves to a Suno channel would run kling.TaskAdaptor against the Suno
// channel's base URL and key. sora accepts both its own ChannelType and
// ChannelTypeOpenAI because relay_adaptor.go's GetTaskAdaptor resolves both
// to the same tasksora.TaskAdaptor (the OpenAI-typed /v1/videos path).
var genericTaskChannelTypes = map[string][]int{
	"suno":   {constant.ChannelTypeSunoAPI},
	"music":  {constant.ChannelTypeSunoAPI},
	"ali":    {constant.ChannelTypeAli},
	"kling":  {constant.ChannelTypeKling},
	"jimeng": {constant.ChannelTypeJimeng},
	"vertex": {constant.ChannelTypeVertexAi},
	"vidu":   {constant.ChannelTypeVidu},
	"doubao": {constant.ChannelTypeDoubaoVideo},
	"sora":   {constant.ChannelTypeSora, constant.ChannelTypeOpenAI},
	"gemini": {constant.ChannelTypeGemini},
	"hailuo": {constant.ChannelTypeMiniMax},
}

// genericTaskPlatformAliases extends the GET status route's ownership match
// (taskPlatformMatchesRoute) beyond genericTaskPlatforms' single key for
// routes where GetTaskAdaptor resolves more than one stored
// constant.TaskPlatform value to the same adaptor. Today only sora: a task
// submitted through /v1/videos on an OpenAI-typed channel is stored with
// Platform == the OpenAI ChannelType (relay_adaptor.go's GetTaskAdaptor
// maps both ChannelTypeSora and ChannelTypeOpenAI to tasksora.TaskAdaptor),
// so GET /v1/tasks/sora/:task_id must accept either stored value.
var genericTaskPlatformAliases = map[string][]constant.TaskPlatform{
	"sora": {
		constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeSora)),
		constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeOpenAI)),
	},
}

// resolveGenericTaskPlatform validates name against the static name table
// above AND against relay.GetTaskAdaptor itself, so a future adaptor
// removal (or a typo in the table) fails closed here instead of reaching
// RelayTaskSubmit's own "invalid_api_platform" 400 further down the chain.
func resolveGenericTaskPlatform(name string) (constant.TaskPlatform, bool) {
	key, ok := genericTaskPlatforms[name]
	if !ok {
		return "", false
	}
	if relay.GetTaskAdaptor(key) == nil {
		return "", false
	}
	return key, true
}

// taskPlatformMatchesRoute reports whether a stored task row's Platform is
// a legitimate match for the :platform URL segment name, accounting for
// genericTaskPlatformAliases (see its doc comment).
func taskPlatformMatchesRoute(name string, taskPlatform constant.TaskPlatform) bool {
	if aliases, ok := genericTaskPlatformAliases[name]; ok {
		for _, alias := range aliases {
			if alias == taskPlatform {
				return true
			}
		}
		return false
	}
	key, ok := genericTaskPlatforms[name]
	return ok && key == taskPlatform
}

func abortTaskPlatformUnknown(c *gin.Context, name string) {
	c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
		"error": types.OpenAIError{
			Message: "Unknown task platform: " + name,
			Type:    types.WireErrorType(http.StatusNotFound, types.ErrorTypeOpenAIError),
			Code:    string(types.ErrorCodeTaskPlatformUnknown),
		},
	})
}

// respondTaskNotFound is the fail-closed 404 for GET /v1/tasks/:platform/:task_id:
// a task belonging to another user and an absent task_id return the
// byte-identical body, so the route cannot be used to probe which task ids
// exist. Deliberately NOT video_proxy.go's 403 — that handler predates this
// lane, stays untouched for its existing consumers, and keeps its own
// existence-leaking (but documented) shape.
func respondTaskNotFound(c *gin.Context) {
	c.JSON(http.StatusNotFound, gin.H{
		"error": gin.H{
			"type":    "invalid_request_error",
			"message": "Task not found",
		},
	})
}

// TaskPlatformGuard validates :platform before PoolBalanceCheck/Distribute
// run (so an unknown platform never reaches channel selection or spends a
// cost-spike/rate-limit budget), and — for suno only — copies the generic
// envelope's "action" body field onto c.Param("action"), because
// suno.TaskAdaptor.ValidateRequestAndSetAction reads that param directly
// (this route has no dedicated :action URL segment; the other ten adaptors
// determine their action from the request body itself, same as their
// dedicated routes).
func TaskPlatformGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		name := c.Param("platform")
		key, ok := resolveGenericTaskPlatform(name)
		if !ok {
			abortTaskPlatformUnknown(c, name)
			return
		}
		c.Set("platform", string(key))
		if key == constant.TaskPlatformSuno && c.Request.Method == http.MethodPost {
			var probe struct {
				Action string `json:"action"`
			}
			if err := common.UnmarshalBodyReusable(c, &probe); err == nil && probe.Action != "" {
				c.Params = append(c.Params, gin.Param{Key: "action", Value: probe.Action})
			}
		}
		c.Next()
	}
}

// TaskChannelPlatformGuard runs after middleware.Distribute() on the POST
// leg only (Distribute is what populates the "channel_type" context key —
// see SetupContextForSelectedChannel in distributor.go). It rejects a
// submit whose declared :platform does not match the channel Distribute
// actually selected for the body's "model", BEFORE handler.RelayTask makes
// any upstream call — see genericTaskChannelTypes' doc comment for why this
// check exists at all.
func TaskChannelPlatformGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		name := c.Param("platform")
		allowed, ok := genericTaskChannelTypes[name]
		if !ok {
			// TaskPlatformGuard already rejected any name not in
			// genericTaskPlatforms upstream of this middleware; fail
			// closed rather than open if that ever changes.
			abortTaskPlatformUnknown(c, name)
			return
		}
		channelType := common.GetContextKeyInt(c, constant.ContextKeyChannelType)
		for _, t := range allowed {
			if t == channelType {
				c.Next()
				return
			}
		}
		// The platform name is valid; the channel the model resolved to is
		// not one that serves it. Saying "unknown platform" here would send
		// the caller looking for a typo in a URL that is correct.
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"error": types.OpenAIError{
				Message: "No channel available for task platform: " + name,
				Type:    types.WireErrorType(http.StatusNotFound, types.ErrorTypeOpenAIError),
				Code:    string(types.ErrorCodeTaskPlatformUnknown),
			},
		})
	}
}

// genericTaskStatusResponse is the GET /v1/tasks/:platform/:task_id 200
// body: relay.TaskModel2Dto's existing curated projection (the same one the
// dedicated /suno/fetch, /v1/audio/music/:task_id and /v1/videos/:task_id
// routes return) plus the three fields those routes don't carry but this
// bearer-token surface documents — platform, project_id, request_id.
// Deliberately does NOT expose channel_id, user_id, quota, group or
// properties: those are internal billing/routing fields, not part of any
// dedicated fetch route's response shape either. See relay.json's
// documented schema for '/v1/tasks/{platform}/{task_id}'.
type genericTaskStatusResponse struct {
	*dto.TaskDto
	Platform  string `json:"platform"`
	ProjectId int    `json:"project_id"`
	RequestId string `json:"request_id"`
}

// GetTaskGeneric is GET /v1/tasks/:platform/:task_id. It reads straight off
// the tasks table — see the file comment for why this does not dispatch
// through RelayTask/RelayTaskFetch like the dedicated fetch routes do.
//
// Ownership is fail-closed by construction, not by role check: TokenAuth
// (the only auth middleware mounted on this route — see task-router.go)
// never sets a "role" context key, so this route has no privileged/admin
// read path to be dead code in the first place. repo.GetByTaskId scopes the
// query itself to (user_id, task_id), so a foreign task sharing a task_id
// with one of the requester's own tasks (task_id is an index, not a unique
// constraint — vendor-issued ids are not guaranteed globally unique) can
// never shadow the requester's row.
func GetTaskGeneric(c *gin.Context) {
	name := c.Param("platform")
	taskID := c.Param("task_id")
	requesterID := c.GetInt("id")

	task, exists, err := repo.GetByTaskId(requesterID, taskID)
	if err != nil {
		logger.LogError(c.Request.Context(), "GetTaskGeneric: query task "+taskID+" failed: "+err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"type": "server_error", "message": "Failed to query task"},
		})
		return
	}
	if !exists || task == nil || !taskPlatformMatchesRoute(name, task.Platform) {
		respondTaskNotFound(c)
		return
	}

	common.ApiSuccess(c, genericTaskStatusResponse{
		TaskDto:   relay.TaskModel2Dto(task),
		Platform:  name,
		ProjectId: task.ProjectId,
		RequestId: task.RequestId,
	})
}
