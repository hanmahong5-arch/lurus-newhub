package handler

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/hub"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const routeAttemptOutcomeSelectError = "channel_select_error"

// Relay dispatches an inference request across candidate upstream channels with circuit-breaker failover.
func Relay(c *gin.Context, relayFormat types.RelayFormat) {
	requestStart := time.Now()
	requestId := c.GetString(common.RequestIdKey)

	var (
		newAPIError *types.NewAPIError
		ws          *websocket.Conn
		relayInfo   *relaycommon.RelayInfo
	)

	defer func() {
		provider := constant.GetChannelTypeName(c.GetInt("channel_type"))
		if provider == "" {
			provider = "unknown"
		}
		model := c.GetString("original_model")
		if model == "" {
			model = "unknown"
		}
		observeRelayOutcome(provider, model, relayInfo, newAPIError, time.Since(requestStart).Seconds(), true)
	}()

	if relayFormat == types.RelayFormatOpenAIRealtime {
		var err error
		ws, err = upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			helper.WssError(c, ws, types.NewError(err, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry()).ToOpenAIError())
			return
		}
		defer ws.Close()
	}

	defer func() {
		if newAPIError != nil {
			renderRelayTerminalError(c, requestId, relayFormat, ws, relayInfo, newAPIError)
		}
	}()

	if newAPIError = applyContentRules(c, relayFormat); newAPIError != nil {
		return
	}
	request, err := helper.GetAndValidateRequest(c, relayFormat)
	if err != nil {
		if common.IsRequestBodyTooLargeError(err) || errors.Is(err, common.ErrRequestBodyTooLarge) {
			newAPIError = types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusRequestEntityTooLarge, types.ErrOptionWithSkipRetry())
		} else {
			newAPIError = types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest)
		}
		return
	}

	relayInfo, newAPIError = prepareRelayContextAndQuota(c, relayFormat, request, ws)
	if newAPIError != nil {
		return
	}

	defer func() {
		releasePreConsumedOnFailure(c, newAPIError, relayInfo)
	}()

	retryParam := &app.RetryParam{
		Ctx:        c,
		TokenGroup: relayInfo.TokenGroup,
		ModelName:  relayInfo.OriginModelName,
		Retry:      common.GetPointer(0),
	}

	attempted := false

	for ; retryParam.GetRetry() <= common.RetryTimes; retryParam.IncreaseRetry() {
		if retryParam.GetRetry() == 0 {
			metrics.RecordRelayOverhead(time.Since(requestStart).Seconds())
		}
		channelSelectStart := time.Now()
		channel, channelErr := getChannelFn(c, relayInfo, retryParam)
		metrics.ChannelSelectDuration.Observe(time.Since(channelSelectStart).Seconds())

		if channelErr != nil {
			logger.LogError(c, channelErr.Error())
			if channelErr.GetErrorCode() == types.ErrorCodeAllChannelsCooling && newAPIError != nil {
				break
			}
			newAPIError = channelErr
			retriesLeft := common.RetryTimes - retryParam.GetRetry()
			if types.IsChannelError(channelErr) && retriesLeft > 0 && shouldRetry(c, channelErr, retriesLeft) {
				recordChannelSelectFailover(c, channelErr)
				continue
			}
			break
		}

		addUsedChannel(c, channel.Id)
		if !channelBreakers.Allow(channel.Id) {
			recordBreakerOpenSkip(c, channel)
			continue
		}

		attempted = true
		c.Set("upstream_request_id", "")
		requestBody, bodyErr := common.GetRequestBody(c)
		if bodyErr != nil {
			if common.IsRequestBodyTooLargeError(bodyErr) || errors.Is(bodyErr, common.ErrRequestBodyTooLarge) {
				newAPIError = types.NewErrorWithStatusCode(bodyErr, types.ErrorCodeReadRequestBodyFailed, http.StatusRequestEntityTooLarge, types.ErrOptionWithSkipRetry())
			} else {
				newAPIError = types.NewErrorWithStatusCode(bodyErr, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			}
			reportBreakerOutcomeCtx(c.Request.Context(), channel.Id, newAPIError, relayInfo.StreamEndReason)
			break
		}
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))

		var relayDuration float64
		newAPIError, relayDuration = executeChannelRelayAttempt(c, channel, relayFormat, relayInfo)
		observeRelayOutcome(constant.GetChannelTypeName(channel.Type), relayInfo.OriginModelName, relayInfo, newAPIError, relayDuration, false)
		reportBreakerOutcomeCtx(c.Request.Context(), channel.Id, newAPIError, relayInfo.StreamEndReason)

		if newAPIError == nil {
			hub.RecordRelayOutcome(channel.Id, true, relayDuration,
				c.GetString("tenant_id"),
				relayInfo.OriginModelName,
				int64(relayInfo.GetEstimatePromptTokens()), 0,
				int64(relayInfo.FinalPreConsumedQuota), 0)
			return
		}

		hub.RecordRelayOutcome(channel.Id, false, relayDuration,
			c.GetString("tenant_id"),
			relayInfo.OriginModelName, 0, 0, 0, 0)
		processChannelError(c, *types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, common.GetContextKeyString(c, constant.ContextKeyChannelKey), channel.GetAutoBan()), newAPIError)

		if !shouldRetry(c, newAPIError, common.RetryTimes-retryParam.GetRetry()) {
			break
		}

		providerName := constant.GetChannelTypeName(channel.Type)
		if types.IsUpstreamFailure(newAPIError) {
			metrics.RecordRelayFailover(providerName, "upstream_error")
		}
		metrics.RetryAttempts.WithLabelValues(providerName, string(newAPIError.GetErrorCode())).Inc()
	}

	if !attempted && newAPIError == nil {
		c.Header("Retry-After", "30")
		newAPIError = types.NewErrorWithStatusCode(
			errors.New("all upstream channels for this model are temporarily unavailable (circuit breakers open)"),
			types.ErrorCodeChannelNoAvailableKey, http.StatusServiceUnavailable,
			types.ErrOptionWithSkipRetry())
	}

	useChannel := c.GetStringSlice("use_channel")
	if len(useChannel) > 1 {
		retryLogStr := fmt.Sprintf("重试：%s", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(useChannel)), "->"), "[]"))
		logger.LogInfo(c, retryLogStr)
	}
}
