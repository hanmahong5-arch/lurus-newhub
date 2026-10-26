package bifrosteng

import (
	"fmt"
	"net/http"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
	"github.com/gin-gonic/gin"
	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
)

// DispatchChat runs a non-stream or stream chat completion via Bifrost and
// writes the OpenAI-compatible response to the gin writer. Caller still runs
// PreConsume (before) and PostConsume (after, using the returned Usage).
//
// Fallbacks are intentionally empty: newhub's outer channel retry remains the
// sole retry/failover layer (no double-retry with Bifrost MaxRetries=0).
func DispatchChat(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (*dto.Usage, *types.NewAPIError) {
	started := time.Now()
	acc := Account()
	cli := Client()
	if acc == nil || cli == nil {
		ObserveEngine(EngineLegacy, ModeChat, "bifrost_unready")
		return nil, types.NewError(fmt.Errorf("bifrosteng: not ready"), types.ErrorCodeDoRequestFailed, types.ErrOptionWithSkipRetry())
	}

	providerKey, err := acc.RegisterChannel(info)
	if err != nil {
		ObserveEngine(EngineBifrost, ModeChat, "unmapped")
		return nil, types.NewError(err, types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}

	msgs, err := toBifrostMessages(request.Messages)
	if err != nil {
		ObserveEngine(EngineBifrost, ModeChat, "bad_request")
		return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	model := info.UpstreamModelName
	if model == "" {
		model = request.Model
	}

	bfReq := &schemas.BifrostChatRequest{
		Provider:  providerKey,
		Model:     model,
		Input:     msgs,
		Params:    toChatParams(request),
		Fallbacks: nil, // outer newhub retry owns failover
	}

	parent := c.Request.Context()
	bfCtx := schemas.NewBifrostContext(parent, schemas.NoDeadline)

	if request.Stream {
		usage, apiErr := dispatchChatStream(c, info, cli, bfCtx, bfReq, model)
		if apiErr != nil {
			observeBifrost(ModeChat, "error", started)
			ObserveEngine(EngineBifrost, ModeChat, "error")
			return nil, apiErr
		}
		observeBifrost(ModeChat, "ok", started)
		ObserveEngine(EngineBifrost, ModeChat, "ok")
		info.IsStream = true
		return usage, nil
	}

	resp, bfErr := cli.ChatCompletionRequest(bfCtx, bfReq)
	if bfErr != nil {
		observeBifrost(ModeChat, "error", started)
		ObserveEngine(EngineBifrost, ModeChat, "error")
		return nil, types.NewOpenAIError(
			fmt.Errorf("%s", bifrostErrMessage(bfErr)),
			types.ErrorCodeDoRequestFailed,
			bifrostErrStatus(bfErr),
		)
	}

	out := toOpenAITextResponse(resp, model)
	c.JSON(http.StatusOK, out)
	observeBifrost(ModeChat, "ok", started)
	ObserveEngine(EngineBifrost, ModeChat, "ok")
	return fromBifrostUsage(resp.Usage), nil
}

func dispatchChatStream(
	c *gin.Context,
	info *relaycommon.RelayInfo,
	cli *bifrost.Bifrost,
	bfCtx *schemas.BifrostContext,
	bfReq *schemas.BifrostChatRequest,
	model string,
) (*dto.Usage, *types.NewAPIError) {
	ch, bfErr := cli.ChatCompletionStreamRequest(bfCtx, bfReq)
	if bfErr != nil {
		return nil, types.NewOpenAIError(
			fmt.Errorf("%s", bifrostErrMessage(bfErr)),
			types.ErrorCodeDoRequestFailed,
			bifrostErrStatus(bfErr),
		)
	}

	helper.SetEventStreamHeaders(c, info)
	var lastUsage *dto.Usage
	for chunk := range ch {
		if chunk == nil {
			continue
		}
		if chunk.BifrostError != nil {
			return lastUsage, types.NewOpenAIError(
				fmt.Errorf("%s", bifrostErrMessage(chunk.BifrostError)),
				types.ErrorCodeDoRequestFailed,
				bifrostErrStatus(chunk.BifrostError),
			)
		}
		if chunk.BifrostChatResponse == nil {
			continue
		}
		sse := toStreamChunk(chunk.BifrostChatResponse, model)
		if sse.Usage != nil {
			lastUsage = sse.Usage
		}
		if err := helper.ObjectData(c, sse); err != nil {
			return lastUsage, types.NewError(err, types.ErrorCodeDoRequestFailed)
		}
	}
	helper.Done(c)
	if lastUsage == nil {
		lastUsage = &dto.Usage{}
	}
	return lastUsage, nil
}
