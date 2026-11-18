package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func renderRelayTerminalError(
	c *gin.Context,
	requestId string,
	relayFormat types.RelayFormat,
	ws *websocket.Conn,
	relayInfo *relaycommon.RelayInfo,
	newAPIError *types.NewAPIError,
) {
	logger.LogError(c, fmt.Sprintf("relay error: %s", newAPIError.Error()))

	if helper.IsIncompleteStreamSurfaced(newAPIError) && c.Writer.Written() {
		return
	}

	recordTerminalRelayError(c, newAPIError)

	provider := constant.GetChannelTypeName(c.GetInt("channel_type"))
	if provider == "" {
		provider = "unknown"
	}
	model := c.GetString("original_model")
	if model == "" {
		model = "unknown"
	}
	errProduct := "unknown"
	if relayInfo != nil {
		errProduct = relayInfo.SourceProduct
	}
	metrics.RecordRelayError(provider, model, types.RelayErrorType(newAPIError), errProduct)

	if types.IsUpstreamFailure(newAPIError) {
		newAPIError.SetMessage(fmt.Sprintf("upstream provider %s returned %d: %s",
			provider, newAPIError.StatusCode, newAPIError.Error()))
	}

	newAPIError.SetMessage(common.MessageWithRequestId(newAPIError.Error(), requestId))

	if newAPIError.GetErrorCode() == types.ErrorCodeChannelAllKeysCooling && newAPIError.RetryAfterUnix > 0 {
		secs := newAPIError.RetryAfterUnix - time.Now().Unix()
		if secs < 1 {
			secs = 30
		}
		c.Header("Retry-After", strconv.FormatInt(secs, 10))
		newAPIError.StatusCode = http.StatusServiceUnavailable
		newAPIError.SetMessage(common.MessageWithRequestId(
			fmt.Sprintf("All keys of the selected channel are rate-limited; retry in ~%ds", secs),
			requestId,
		))
	}

	if newAPIError.RetryAfterUnix > 0 {
		if secs := newAPIError.RetryAfterUnix - time.Now().Unix(); secs > 0 {
			c.Header("Retry-After", strconv.FormatInt(secs, 10))
		}
	}

	if isUpstreamOriginatedError(newAPIError) {
		middleware.ClearRateLimitHeadroomHeaders(c)
		if c.Writer.Header().Get("Retry-After") == "" && newAPIError.UpstreamHeader != nil {
			if ra := newAPIError.UpstreamHeader.Get("Retry-After"); ra != "" {
				c.Header("Retry-After", ra)
			}
		}
	}

	if relayFormat == types.RelayFormatOpenAIRealtime {
		helper.WssError(c, ws, newAPIError.ToOpenAIError())
		return
	}

	if c.Writer.Written() {
		helper.StreamError(c, relayFormat, newAPIError)
		return
	}

	switch relayFormat {
	case types.RelayFormatClaude:
		c.JSON(newAPIError.StatusCode, gin.H{
			"type":  "error",
			"error": newAPIError.ToClaudeError(),
		})
	case types.RelayFormatGemini:
		c.JSON(newAPIError.StatusCode, newAPIError.ToGeminiError())
	default:
		c.JSON(newAPIError.StatusCode, gin.H{
			"error": newAPIError.ToOpenAIError(),
		})
	}
}

func isUpstreamOriginatedError(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	return types.IsUpstreamFailure(err) || err.StatusCode == http.StatusTooManyRequests
}
