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
package relay

import (
	"fmt"
	"net/http"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/relay/bifrosteng"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/model_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
	"github.com/gin-gonic/gin"
)

// tryBifrostClaude dispatches /v1/messages via in-process Bifrost when the
// messages mode is on and the request converts cleanly. Returns handled=false
// to fall through to the legacy Claude adaptor.
func tryBifrostClaude(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (handled bool, apiErr *types.NewAPIError) {
	passThrough := model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled
	if !bifrosteng.ShouldHandleMessages(info.ApiType, passThrough) {
		return false, nil
	}

	oai, err := app.ClaudeToOpenAIRequest(*request, info)
	if err != nil {
		return true, types.NewError(fmt.Errorf("bifrost claude convert: %w", err), types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	if !bifrosteng.ChatConvertible(oai) {
		return false, nil
	}
	// Keep UpstreamModelName aligned with the mapped Claude model.
	if info.UpstreamModelName != "" {
		oai.Model = info.UpstreamModelName
	}

	if request.Stream {
		ch, streamErr := bifrosteng.StreamChat(c, info, oai, bifrosteng.ModeMessages)
		if streamErr != nil {
			return true, streamErr
		}
		helper.SetEventStreamHeaders(c, info)
		var lastUsage *dto.Usage
		for ev := range ch {
			if ev.Err != nil {
				return true, ev.Err
			}
			if ev.SSE == nil {
				continue
			}
			if ev.SSE.Usage != nil {
				lastUsage = ev.SSE.Usage
			}
			for _, claudeEv := range app.StreamResponseOpenAI2Claude(ev.SSE, info) {
				if err := helper.ObjectData(c, claudeEv); err != nil {
					return true, types.NewError(err, types.ErrorCodeDoRequestFailed)
				}
			}
		}
		helper.Done(c)
		info.IsStream = true
		if lastUsage == nil {
			lastUsage = &dto.Usage{}
		}
		app.PostClaudeConsumeQuota(c, info, lastUsage)
		return true, nil
	}

	textResp, usage, completeErr := bifrosteng.CompleteChat(c, info, oai, bifrosteng.ModeMessages)
	if completeErr != nil {
		return true, completeErr
	}
	claudeResp := app.ResponseOpenAI2Claude(textResponseToOpenAI(textResp), info)
	c.JSON(http.StatusOK, claudeResp)
	if usage == nil {
		usage = &dto.Usage{}
	}
	app.PostClaudeConsumeQuota(c, info, usage)
	return true, nil
}

func textResponseToOpenAI(t *dto.TextResponse) *dto.OpenAITextResponse {
	if t == nil {
		return &dto.OpenAITextResponse{}
	}
	return &dto.OpenAITextResponse{
		Id:      t.Id,
		Model:   t.Model,
		Object:  t.Object,
		Created: t.Created,
		Choices: t.Choices,
		Usage:   t.Usage,
	}
}
