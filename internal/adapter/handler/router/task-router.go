package router

import (
	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"

	"github.com/gin-gonic/gin"
)

// SetTaskRouter mounts the generic async-task surface (cycle-8 L8):
//
//	POST /v1/tasks/:platform            — submit, dispatches through
//	                                       handler.RelayTask (same billing/
//	                                       InitTask/poller path as the
//	                                       dedicated /suno, /v1/audio/music,
//	                                       /kling/v1, /v1/videos routes).
//	GET  /v1/tasks/:platform/:task_id   — status, a plain ownership-checked
//	                                       read (see task_generic.go).
//
// Middleware chain mirrors relay-router.go's relaySunoRouter/
// relayMusicRouter groups (TokenAuth, PoolBalanceCheck, CostSpikeLimit,
// EntitlementCheck, ModelRequestRateLimit, BusinessRateLimit,
// RelayConcurrencyLimit) with handler.TaskPlatformGuard inserted right
// after TokenAuth — before any of the spend-tracking middlewares run, so
// an unknown :platform is rejected before it can consume a cost-spike or
// rate-limit budget. Distribute() is added ONLY on the POST leg: GET does
// not select a channel (see task_generic.go's GetTaskGeneric doc comment),
// and distributor.go's getModelRequest has no /v1/tasks branch to short-
// circuit channel selection for a bodyless GET the way it does for the
// dedicated fetch routes. handler.TaskChannelPlatformGuard runs immediately
// after Distribute() on the POST leg: it rejects a declared :platform that
// does not match the channel Distribute actually selected, before any
// upstream call (see its doc comment in task_generic.go).
func SetTaskRouter(router *gin.Engine) {
	taskRouter := router.Group("/v1/tasks")
	taskRouter.Use(
		middleware.TokenAuth(),
		handler.TaskPlatformGuard(),
		middleware.PoolBalanceCheck(),
		middleware.CostSpikeLimit(),
		middleware.EntitlementCheck(),
		middleware.ModelRequestRateLimit(),
		middleware.BusinessRateLimit(),
		middleware.RelayConcurrencyLimit(),
	)
	{
		taskRouter.POST("/:platform", middleware.Distribute(), handler.TaskChannelPlatformGuard(), handler.RelayTask)
		taskRouter.GET("/:platform/:task_id", handler.GetTaskGeneric)
		// Cycle-8 L9 (tasks-plugins-02/17/19): metadata-only artefact
		// listing and a content proxy for one artefact, generalising
		// video_proxy.go beyond video. Same chain as the GET status route
		// above (no Distribute — neither lists nor fetches select a
		// channel), same fail-closed 404-for-not-found-or-not-owned outcome
		// (task_artifacts.go's loadOwnedTask — see its doc comment for how
		// its lookup differs from GetTaskGeneric's).
		taskRouter.GET("/:platform/:task_id/artifacts", handler.ListTaskArtifacts)
		taskRouter.GET("/:platform/:task_id/artifacts/:key/content", handler.GetTaskArtifactContent)
	}
}
