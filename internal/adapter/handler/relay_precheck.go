package handler

import (
	"fmt"
	"net/http"
	"strings"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func prepareRelayContextAndQuota(
	c *gin.Context,
	relayFormat types.RelayFormat,
	request dto.Request,
	ws *websocket.Conn,
) (*relaycommon.RelayInfo, *types.NewAPIError) {
	relayInfo, err := relaycommon.GenRelayInfo(c, relayFormat, request, ws)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeGenRelayInfoFailed)
	}

	governance.EnrichContext(c, relayInfo.TokenId, relayInfo.OriginModelName)

	if affinityKey := app.DeriveSessionAffinityKey(c, request); affinityKey != "" {
		common.SetContextKey(c, constant.ContextKeySessionAffinity, affinityKey)
	}

	needSensitiveCheck := setting.ShouldCheckPromptSensitive()
	needCountToken := constant.CountToken
	var meta *types.TokenCountMeta
	if needSensitiveCheck || needCountToken {
		meta = request.GetTokenCountMeta()
	} else {
		meta = fastTokenCountMetaForPricing(request)
	}

	if needSensitiveCheck && meta != nil {
		contains, words := app.CheckSensitiveText(meta.CombineText)
		if contains {
			logger.LogWarn(c, fmt.Sprintf("user sensitive words detected: %s", strings.Join(words, ", ")))
			governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorToken,
				relayInfo.UserId, governance.ActionSensitiveBlocked, governance.ResourceUser,
				relayInfo.TokenId, fmt.Sprintf(`{"word_count":%d}`, len(words))))
			return relayInfo, types.NewErrorWithStatusCode(
				fmt.Errorf("request rejected: prompt contains %d blocked term(s)", len(words)),
				types.ErrorCodeSensitiveWordsDetected, http.StatusBadRequest,
				types.ErrOptionWithSkipRetry())
		}
	}

	tokens, err := app.EstimateRequestToken(c, meta, relayInfo)
	if err != nil {
		return relayInfo, types.NewError(err, types.ErrorCodeCountTokenFailed)
	}
	relayInfo.SetEstimatePromptTokens(tokens)

	priceData, err := helper.ModelPriceHelper(c, relayInfo, tokens, meta)
	if err != nil {
		return relayInfo, types.NewError(err, types.ErrorCodeModelPriceError)
	}

	if priceData.FreeModel {
		logger.LogInfo(c, fmt.Sprintf("模型 %s 免费，跳过预扣费", relayInfo.OriginModelName))
		return relayInfo, nil
	}

	if apiErr := app.PreConsumeQuota(c, priceData.QuotaToPreConsume, relayInfo); apiErr != nil {
		return relayInfo, apiErr
	}
	return relayInfo, nil
}
