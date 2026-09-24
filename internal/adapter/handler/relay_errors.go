package handler

import (
	"fmt"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/openrouter_pool"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func processChannelError(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError) {
	logger.LogError(c, fmt.Sprintf("channel error (channel #%d, status code: %d): %s", channelError.ChannelId, err.StatusCode, err.Error()))
	// 不要使用context获取渠道信息，异步处理时可能会出现渠道信息不一致的情况
	// do not use context to get channel info, there may be inconsistent channel info when processing asynchronously
	if app.ShouldDisableChannel(channelError.ChannelType, err) && channelError.AutoBan {
		AsyncGo(func() {
			app.DisableChannel(channelError, err.Error())
		})
	}

	// OpenRouter free-key pool: rate-limited keys get a per-key cooldown rather
	// than being treated as permanently disabled. No-op for non-OpenRouter or non-429.
	AsyncGo(func() {
		openrouter_pool.MaybeMarkCooldown(channelError, err)
	})

	// Channel-stage errors are recorded (or deliberately skipped) HERE, once
	// per attempt; mark that so the terminal-error fallback in Relay's deferred
	// renderer doesn't write the last attempt's error a second time.
	c.Set(relayErrorLogHandledKey, true)
	recordRelayErrorLog(c, err)
}

// relayErrorLogHandledKey marks that processChannelError already owned the
// error-log decision for this request. The deferred renderer in Relay only
// records when the flag is absent — i.e. the error happened BEFORE any channel
// attempt (request validation, token estimation, pricing, channel selection),
// a class that previously left no error-log row at all.
const relayErrorLogHandledKey = "relay_error_log_handled"

// recordTerminalRelayError is the deferred renderer's fallback: it records the
// terminal error ONLY when no channel-stage attempt already owned the logging
// decision, so the last attempt's error is never written twice.
func recordTerminalRelayError(c *gin.Context, err *types.NewAPIError) {
	if c.GetBool(relayErrorLogHandledKey) {
		return
	}
	recordRelayErrorLog(c, err)
}

func recordRelayErrorLog(c *gin.Context, err *types.NewAPIError) {
	if !constant.ErrorLogEnabled || !types.IsRecordErrorLog(err) {
		return
	}
	// 保存错误日志到mysql中
	userId := c.GetInt("id")
	tokenName := c.GetString("token_name")
	modelName := c.GetString("original_model")
	tokenId := c.GetInt("token_id")
	userGroup := c.GetString("group")
	channelId := c.GetInt("channel_id")
	other := make(map[string]interface{})
	if c.Request != nil && c.Request.URL != nil {
		other["request_path"] = c.Request.URL.Path
	}
	other["error_type"] = err.GetErrorType()
	other["error_code"] = err.GetErrorCode()
	other["status_code"] = err.StatusCode
	other["channel_id"] = channelId
	other["channel_name"] = c.GetString("channel_name")
	other["channel_type"] = c.GetInt("channel_type")
	other["relay_mode"] = c.GetInt("relay_mode")
	// Workstream 0: error rows never went through genBaseRelayInfo when the
	// failure happened before GenRelayInfo ran (e.g. request binding), so
	// RelayInfo.SourceProduct may not exist yet — read the header directly
	// off the request that is still in hand, same resolver as the success path.
	other["source_product"] = ratio_setting.ResolveSourceProduct(c.GetHeader(ratio_setting.SourceProductHeader))
	if upModel := c.GetString("original_model"); upModel != "" {
		other["upstream_model"] = upModel
	}
	// The vendor's own request/trace id, set by provider.doRequest via
	// c.Set beside its RelayInfo.UpstreamRequestId write — this is how a
	// 5xx from upstream still gets it onto the error row without a new
	// parameter on processChannelError/recordTerminalRelayError. Absent
	// when the failure happened before any channel attempt reached
	// doRequest (request validation, channel selection, etc).
	if upstreamReqId := c.GetString("upstream_request_id"); upstreamReqId != "" {
		other["upstream_request_id"] = upstreamReqId
	}
	adminInfo := make(map[string]interface{})
	adminInfo["use_channel"] = c.GetStringSlice("use_channel")
	isMultiKey := common.GetContextKeyBool(c, constant.ContextKeyChannelIsMultiKey)
	if isMultiKey {
		adminInfo["is_multi_key"] = true
		adminInfo["multi_key_index"] = common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex)
	}
	other["admin_info"] = adminInfo
	repo.RecordErrorLog(c, userId, channelId, modelName, tokenName, err.MaskSensitiveError(), tokenId, 0, false, userGroup, other)
}
