/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
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

// StreamChunk is one OpenAI-shaped SSE piece (or a terminal stream error).
type StreamChunk struct {
	SSE *dto.ChatCompletionsStreamResponse
	Err *types.NewAPIError
}

// buildChatRequest registers the channel and builds a Bifrost chat request.
// Fallbacks stay empty: newhub's outer channel retry owns failover.
func buildChatRequest(info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest, modality string) (
	cli *bifrost.Bifrost,
	bfReq *schemas.BifrostChatRequest,
	model string,
	apiErr *types.NewAPIError,
) {
	acc := Account()
	cli = Client()
	if acc == nil || cli == nil {
		ObserveEngine(EngineLegacy, modality, "bifrost_unready")
		return nil, nil, "", types.NewError(fmt.Errorf("bifrosteng: not ready"), types.ErrorCodeDoRequestFailed, types.ErrOptionWithSkipRetry())
	}
	providerKey, err := acc.RegisterChannel(info)
	if err != nil {
		ObserveEngine(EngineBifrost, modality, "unmapped")
		return nil, nil, "", types.NewError(err, types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	msgs, err := toBifrostMessages(request.Messages)
	if err != nil {
		ObserveEngine(EngineBifrost, modality, "bad_request")
		return nil, nil, "", types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	model = info.UpstreamModelName
	if model == "" {
		model = request.Model
	}
	bfReq = &schemas.BifrostChatRequest{
		Provider:  providerKey,
		Model:     model,
		Input:     msgs,
		Params:    toChatParams(request),
		Fallbacks: nil,
	}
	return cli, bfReq, model, nil
}

// CompleteChat runs a non-stream Bifrost chat and returns the OpenAI-shaped
// body without writing to gin (Claude /v1/messages re-encodes the wire).
func CompleteChat(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest, modality string) (*dto.TextResponse, *dto.Usage, *types.NewAPIError) {
	if modality == "" {
		modality = ModeChat
	}
	started := time.Now()
	cli, bfReq, model, apiErr := buildChatRequest(info, request, modality)
	if apiErr != nil {
		return nil, nil, apiErr
	}
	bfCtx := schemas.NewBifrostContext(c.Request.Context(), schemas.NoDeadline)
	resp, bfErr := cli.ChatCompletionRequest(bfCtx, bfReq)
	if bfErr != nil {
		observeBifrost(modality, "error", started)
		ObserveEngine(EngineBifrost, modality, "error")
		return nil, nil, types.NewOpenAIError(
			fmt.Errorf("%s", bifrostErrMessage(bfErr)),
			types.ErrorCodeDoRequestFailed,
			bifrostErrStatus(bfErr),
		)
	}
	out := toOpenAITextResponse(resp, model)
	observeBifrost(modality, "ok", started)
	ObserveEngine(EngineBifrost, modality, "ok")
	return out, fromBifrostUsage(resp.Usage), nil
}

// StreamChat opens a Bifrost chat stream; caller owns SSE framing / wire format.
func StreamChat(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest, modality string) (
	<-chan StreamChunk,
	*types.NewAPIError,
) {
	if modality == "" {
		modality = ModeChat
	}
	started := time.Now()
	cli, bfReq, model, apiErr := buildChatRequest(info, request, modality)
	if apiErr != nil {
		return nil, apiErr
	}
	bfCtx := schemas.NewBifrostContext(c.Request.Context(), schemas.NoDeadline)
	ch, bfErr := cli.ChatCompletionStreamRequest(bfCtx, bfReq)
	if bfErr != nil {
		observeBifrost(modality, "error", started)
		ObserveEngine(EngineBifrost, modality, "error")
		return nil, types.NewOpenAIError(
			fmt.Errorf("%s", bifrostErrMessage(bfErr)),
			types.ErrorCodeDoRequestFailed,
			bifrostErrStatus(bfErr),
		)
	}
	out := make(chan StreamChunk)
	go func() {
		defer close(out)
		status := "ok"
		defer func() {
			observeBifrost(modality, status, started)
			ObserveEngine(EngineBifrost, modality, status)
		}()
		for chunk := range ch {
			if chunk == nil {
				continue
			}
			if chunk.BifrostError != nil {
				status = "error"
				out <- StreamChunk{Err: types.NewOpenAIError(
					fmt.Errorf("%s", bifrostErrMessage(chunk.BifrostError)),
					types.ErrorCodeDoRequestFailed,
					bifrostErrStatus(chunk.BifrostError),
				)}
				return
			}
			if chunk.BifrostChatResponse == nil {
				continue
			}
			out <- StreamChunk{SSE: toStreamChunk(chunk.BifrostChatResponse, model)}
		}
	}()
	return out, nil
}

// DispatchChat runs a non-stream or stream chat completion via Bifrost and
// writes the OpenAI-compatible response to the gin writer. Caller still runs
// PreConsume (before) and PostConsume (after, using the returned Usage).
func DispatchChat(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (*dto.Usage, *types.NewAPIError) {
	if request.Stream {
		ch, apiErr := StreamChat(c, info, request, ModeChat)
		if apiErr != nil {
			return nil, apiErr
		}
		helper.SetEventStreamHeaders(c, info)
		var lastUsage *dto.Usage
		for ev := range ch {
			if ev.Err != nil {
				return lastUsage, ev.Err
			}
			if ev.SSE == nil {
				continue
			}
			if ev.SSE.Usage != nil {
				lastUsage = ev.SSE.Usage
			}
			if err := helper.ObjectData(c, ev.SSE); err != nil {
				return lastUsage, types.NewError(err, types.ErrorCodeDoRequestFailed)
			}
		}
		helper.Done(c)
		info.IsStream = true
		if lastUsage == nil {
			lastUsage = &dto.Usage{}
		}
		return lastUsage, nil
	}

	out, usage, apiErr := CompleteChat(c, info, request, ModeChat)
	if apiErr != nil {
		return nil, apiErr
	}
	c.JSON(http.StatusOK, out)
	return usage, nil
}
