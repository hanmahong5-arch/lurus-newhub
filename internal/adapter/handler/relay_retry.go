package handler

import (
	"fmt"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

var getChannelFn = getChannel

func getChannel(c *gin.Context, info *relaycommon.RelayInfo, retryParam *app.RetryParam) (*repo.Channel, *types.NewAPIError) {
	if info.ChannelMeta == nil {
		autoBan := c.GetBool("auto_ban")
		autoBanInt := 1
		if !autoBan {
			autoBanInt = 0
		}
		return &repo.Channel{
			Id:      c.GetInt("channel_id"),
			Type:    c.GetInt("channel_type"),
			Name:    c.GetString("channel_name"),
			AutoBan: &autoBanInt,
		}, nil
	}
	if tc, terr := middleware.GetTenantContext(c); terr == nil && tc != nil {
		retryParam.TenantID = tc.TenantID
	}
	channel, selectGroup, err := app.CacheGetRandomSatisfiedChannel(retryParam)

	info.PriceData.GroupRatioInfo = helper.HandleGroupRatio(c, info)

	if err != nil {
		return nil, channelSelectionError(err, info.OriginModelName, selectGroup)
	}
	if channel == nil {
		return nil, types.NewError(fmt.Errorf("no available channel for model %s in group %s (retry)", info.OriginModelName, selectGroup), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}

	newAPIError := middleware.SetupContextForSelectedChannel(c, channel, info.OriginModelName)
	if newAPIError != nil {
		return nil, newAPIError
	}
	return channel, nil
}

func recordChannelSelectFailover(c *gin.Context, channelErr *types.NewAPIError) {
	failedID := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	failedProvider := constant.GetChannelTypeName(common.GetContextKeyInt(c, constant.ContextKeyChannelType))
	app.RecordRouteAttempt(c, app.RouteAttempt{
		ChannelID:   failedID,
		ChannelName: common.GetContextKeyString(c, constant.ContextKeyChannelName),
		Provider:    failedProvider,
		Outcome:     routeAttemptOutcomeSelectError,
		ErrorCode:   string(channelErr.GetErrorCode()),
		StatusCode:  channelErr.StatusCode,
	})
	metrics.RecordRelayFailover(failedProvider, routeAttemptOutcomeSelectError)
	metrics.RetryAttempts.WithLabelValues(failedProvider, string(channelErr.GetErrorCode())).Inc()
	if failedID != 0 {
		addUsedChannel(c, failedID)
	}
}

func recordBreakerOpenSkip(c *gin.Context, channel *repo.Channel) {
	metrics.RecordCircuitBreakerRejection(fmt.Sprintf("%d", channel.Id))
	metrics.RecordRelayFailover(constant.GetChannelTypeName(channel.Type), "breaker_open")
	app.RecordRouteAttempt(c, app.RouteAttempt{
		ChannelID:   channel.Id,
		ChannelName: channel.Name,
		Provider:    constant.GetChannelTypeName(channel.Type),
		Outcome:     app.RouteAttemptOutcomeBreakerOpen,
	})
	logger.LogDebug(c, "circuit breaker open for channel #%d, skipping", channel.Id)
}

func shouldRetry(c *gin.Context, openaiErr *types.NewAPIError, retryTimes int) bool {
	if openaiErr == nil {
		return false
	}
	if c != nil && c.Writer != nil && c.Writer.Written() {
		metrics.RecordFailoverSuppressed("stream_already_started")
		logger.LogDebug(c, "response already streamed (%d bytes); suppressing failover", c.Writer.Size())
		return false
	}
	if types.IsChannelError(openaiErr) {
		return true
	}
	if types.IsSkipRetryError(openaiErr) {
		return false
	}
	if retryTimes <= 0 {
		return false
	}
	if _, ok := c.Get("specific_channel_id"); ok {
		return false
	}
	if openaiErr.StatusCode == http.StatusTooManyRequests {
		return !app.IsRequestCaused429(openaiErr)
	}
	if openaiErr.StatusCode == 307 {
		return true
	}
	if openaiErr.StatusCode/100 == 5 {
		if openaiErr.StatusCode == 504 || openaiErr.StatusCode == 524 {
			return false
		}
		return true
	}
	if openaiErr.StatusCode == http.StatusBadRequest {
		return isProviderContentRefusal(openaiErr)
	}
	if openaiErr.StatusCode == 408 {
		return false
	}
	if openaiErr.StatusCode/100 == 2 {
		return false
	}
	return true
}
