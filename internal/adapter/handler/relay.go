package handler

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/resilience"
	"github.com/LurusTech/lurus-hub/internal/pkg/tracing"
	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/app/relay"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/hub"
	"github.com/LurusTech/lurus-hub/internal/app/openrouter_pool"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/bytedance/gopkg/util/gopool"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// channelBreakers is the global per-channel circuit breaker registry.
// Initialized with default config (CB_THRESHOLD=5, CB_TIMEOUT_SEC=30).
var channelBreakers = resilience.NewRegistry(func() resilience.Config {
	cfg := resilience.DefaultConfig()
	cfg.OnStateChange = func(channelID int, from, to resilience.State) {
		idStr := fmt.Sprintf("%d", channelID)
		metrics.RecordCircuitBreakerState(idStr, int(to))
		if to == resilience.StateOpen {
			metrics.RecordCircuitBreakerTrip(idStr)
		}
		common.SysLog(fmt.Sprintf("circuit breaker channel #%d: %s → %s", channelID, from, to))
	}
	return cfg
}())

func relayHandler(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	var err *types.NewAPIError
	switch info.RelayMode {
	case relayconstant.RelayModeImagesGenerations, relayconstant.RelayModeImagesEdits:
		err = relay.ImageHelper(c, info)
	case relayconstant.RelayModeAudioSpeech:
		fallthrough
	case relayconstant.RelayModeAudioTranslation:
		fallthrough
	case relayconstant.RelayModeAudioTranscription:
		err = relay.AudioHelper(c, info)
	case relayconstant.RelayModeRerank:
		err = relay.RerankHelper(c, info)
	case relayconstant.RelayModeEmbeddings:
		err = relay.EmbeddingHelper(c, info)
	case relayconstant.RelayModeResponses:
		err = relay.ResponsesHelper(c, info)
	default:
		err = relay.TextHelper(c, info)
	}
	return err
}

func geminiRelayHandler(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	var err *types.NewAPIError
	if strings.Contains(c.Request.URL.Path, "embed") {
		err = relay.GeminiEmbeddingHandler(c, info)
	} else {
		err = relay.GeminiHelper(c, info)
	}
	return err
}

func Relay(c *gin.Context, relayFormat types.RelayFormat) {
	// Capture request start before any work; bounds end-to-end latency.
	requestStart := time.Now()

	requestId := c.GetString(common.RequestIdKey)
	//group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	//originalModel := common.GetContextKeyString(c, constant.ContextKeyOriginalModel)

	var (
		newAPIError *types.NewAPIError
		ws          *websocket.Conn
	)

	// Total-duration defer. Declared first so LIFO runs it LAST,
	// after the error-response defer below sets the final status.
	defer func() {
		provider := constant.GetChannelTypeName(c.GetInt("channel_type"))
		if provider == "" {
			provider = "unknown"
		}
		model := c.GetString("original_model")
		if model == "" {
			model = "unknown"
		}
		status := "success"
		if newAPIError != nil {
			status = "error"
		}
		metrics.RecordRelayTotal(provider, model, status, time.Since(requestStart).Seconds())
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
			logger.LogError(c, fmt.Sprintf("relay error: %s", newAPIError.Error()))

			// Pre-channel failures (request binding/validation, token estimate,
			// pricing, pre-consume, channel selection) never pass through
			// processChannelError, so without this they left no error-log row.
			// Record before the SetMessage mutations below so the row carries
			// the raw masked error, same as channel-stage rows. Errors that
			// opted out via ErrOptionWithNoRecordErrorLog stay out — the check
			// lives inside recordRelayErrorLog.
			recordTerminalRelayError(c, newAPIError)

			// Resolve the last-tried provider once for both the O1 metric and the
			// client-facing provider context below (mirrors the RecordRelayTotal defer).
			provider := constant.GetChannelTypeName(c.GetInt("channel_type"))
			if provider == "" {
				provider = "unknown"
			}
			model := c.GetString("original_model")
			if model == "" {
				model = "unknown"
			}
			// O1 (B2): classify this terminal failure. Runs once per request — the
			// defer body executes only on the error path (success returns without
			// setting newAPIError), so this never double-counts vs RetryAttempts.
			metrics.RecordRelayError(provider, model, types.RelayErrorType(newAPIError))

			// E1 (B4): when the failure is upstream-attributable, tell the client
			// WHICH provider failed and with what status, BEFORE the request-id wrap
			// below. MaskSensitiveInfo still runs in ToOpenAIError/ToClaudeError after
			// this, so no upstream secret leaks; envelope shape/status/code unchanged.
			// The all-keys-cooling branch builds + returns its own message → unaffected.
			if types.IsUpstreamFailure(newAPIError) {
				newAPIError.SetMessage(fmt.Sprintf("upstream provider %s returned %d: %s",
					provider, newAPIError.StatusCode, newAPIError.Error()))
			}

			newAPIError.SetMessage(common.MessageWithRequestId(newAPIError.Error(), requestId))

			// All-keys-cooling (every key in an OpenRouter pool is rate-limited):
			// translate to 503 + Retry-After so clients back off intelligently
			// instead of seeing a misleading 429.
			if newAPIError.GetErrorCode() == types.ErrorCodeChannelAllKeysCooling && newAPIError.RetryAfterUnix > 0 {
				secs := newAPIError.RetryAfterUnix - time.Now().Unix()
				if secs < 1 {
					secs = 30
				}
				c.Header("Retry-After", strconv.FormatInt(secs, 10))
				c.JSON(http.StatusServiceUnavailable, gin.H{
					"error": gin.H{
						"type":    "service_unavailable",
						"code":    string(types.ErrorCodeChannelAllKeysCooling),
						"message": fmt.Sprintf("All keys in the OpenRouter pool are rate-limited; retry in ~%ds", secs),
					},
				})
				return
			}

			// Surface RetryAfterUnix as Retry-After for any error that carries it
			// (e.g. tenant monthly quota exceeded → next-month rollover).
			// Skip when the deadline is already past — absence of header tells the
			// client to retry immediately, which is the desired RFC 7231 semantics.
			if newAPIError.RetryAfterUnix > 0 {
				if secs := newAPIError.RetryAfterUnix - time.Now().Unix(); secs > 0 {
					c.Header("Retry-After", strconv.FormatInt(secs, 10))
				}
			}

			if relayFormat == types.RelayFormatOpenAIRealtime {
				helper.WssError(c, ws, newAPIError.ToOpenAIError())
				return
			}

			// If SSE bytes were already flushed to the client (error surfaced after
			// streaming started), a raw c.JSON blob would corrupt the text/event-stream
			// body for every consumer. Emit the error in-band in the caller's wire
			// shape instead (helper.StreamError: each official SDK recognises exactly
			// one shape, and only that one raises). Only this already-started-stream
			// branch changes; the not-yet-written c.JSON path below is untouched.
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
			default:
				c.JSON(newAPIError.StatusCode, gin.H{
					"error": newAPIError.ToOpenAIError(),
				})
			}
		}
	}()

	request, err := helper.GetAndValidateRequest(c, relayFormat)
	if err != nil {
		// Map "request body too large" to 413 so clients can handle it correctly
		if common.IsRequestBodyTooLargeError(err) || errors.Is(err, common.ErrRequestBodyTooLarge) {
			newAPIError = types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusRequestEntityTooLarge, types.ErrOptionWithSkipRetry())
		} else {
			// 400, not NewError's default 500: this is the caller's malformed
			// body. The default also made IsUpstreamFailure (500 ⇒ true) dress
			// the response up as "upstream provider Unknown returned 500" and
			// counted client garbage toward our 5xx rate.
			newAPIError = types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest)
		}
		return
	}

	relayInfo, err := relaycommon.GenRelayInfo(c, relayFormat, request, ws)
	if err != nil {
		newAPIError = types.NewError(err, types.ErrorCodeGenRelayInfoFailed)
		return
	}

	governance.EnrichContext(c, relayInfo.TokenId, relayInfo.OriginModelName)

	// Derive the conversation binding once, after the body is parsed and the
	// token/group context is populated, so channel selection (and every retry's
	// re-pin) can read it without re-parsing anything.
	if affinityKey := app.DeriveSessionAffinityKey(c, request); affinityKey != "" {
		common.SetContextKey(c, constant.ContextKeySessionAffinity, affinityKey)
	}

	// Workstream 0: resolve the cross-product attribution tag once, here, so it
	// reaches both the wallet (PostConsumeQuota, which has no gin.Context) and
	// the log row (EnrichLogParams). Unknown/absent header → default product.
	relayInfo.SourceProduct = ratio_setting.ResolveSourceProduct(c.GetHeader(ratio_setting.SourceProductHeader))

	needSensitiveCheck := setting.ShouldCheckPromptSensitive()
	needCountToken := constant.CountToken
	// Avoid building huge CombineText (strings.Join) when token counting and sensitive check are both disabled.
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
			newAPIError = types.NewError(err, types.ErrorCodeSensitiveWordsDetected)
			return
		}
	}

	tokens, err := app.EstimateRequestToken(c, meta, relayInfo)
	if err != nil {
		newAPIError = types.NewError(err, types.ErrorCodeCountTokenFailed)
		return
	}

	relayInfo.SetEstimatePromptTokens(tokens)

	priceData, err := helper.ModelPriceHelper(c, relayInfo, tokens, meta)
	if err != nil {
		newAPIError = types.NewError(err, types.ErrorCodeModelPriceError)
		return
	}

	// common.SetContextKey(c, constant.ContextKeyTokenCountMeta, meta)

	if priceData.FreeModel {
		logger.LogInfo(c, fmt.Sprintf("模型 %s 免费，跳过预扣费", relayInfo.OriginModelName))
	} else {
		newAPIError = app.PreConsumeQuota(c, priceData.QuotaToPreConsume, relayInfo)
		if newAPIError != nil {
			return
		}
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

	for ; retryParam.GetRetry() <= common.RetryTimes; retryParam.IncreaseRetry() {
		// One-shot overhead — newhub-side latency budget, excludes retries.
		if retryParam.GetRetry() == 0 {
			metrics.RecordRelayOverhead(time.Since(requestStart).Seconds())
		}
		channelSelectStart := time.Now()
		channel, channelErr := getChannel(c, relayInfo, retryParam)
		metrics.ChannelSelectDuration.Observe(time.Since(channelSelectStart).Seconds())

		if channelErr != nil {
			logger.LogError(c, channelErr.Error())
			newAPIError = channelErr
			break
		}

		// Circuit breaker: skip channels whose breaker is Open to avoid
		// sending traffic to a known-failing upstream provider.
		if !channelBreakers.Allow(channel.Id) {
			metrics.RecordCircuitBreakerRejection(fmt.Sprintf("%d", channel.Id))
			// O2 (B3): skipping an Open-breaker channel is a failover event.
			metrics.RecordRelayFailover(constant.GetChannelTypeName(channel.Type), "breaker_open")
			app.RecordRouteAttempt(c, app.RouteAttempt{
				ChannelID:   channel.Id,
				ChannelName: channel.Name,
				Provider:    constant.GetChannelTypeName(channel.Type),
				Outcome:     app.RouteAttemptOutcomeBreakerOpen,
			})
			logger.LogDebug(c, "circuit breaker open for channel #%d, skipping", channel.Id)
			continue
		}

		addUsedChannel(c, channel.Id)
		requestBody, bodyErr := common.GetRequestBody(c)
		if bodyErr != nil {
			// Ensure consistent 413 for oversized bodies even when error occurs later (e.g., retry path)
			if common.IsRequestBodyTooLargeError(bodyErr) || errors.Is(bodyErr, common.ErrRequestBodyTooLarge) {
				newAPIError = types.NewErrorWithStatusCode(bodyErr, types.ErrorCodeReadRequestBodyFailed, http.StatusRequestEntityTooLarge, types.ErrOptionWithSkipRetry())
			} else {
				newAPIError = types.NewErrorWithStatusCode(bodyErr, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			}
			break
		}
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))

		// Record relay request metrics
		relayStart := time.Now()
		providerName := constant.GetChannelTypeName(channel.Type)

		// Start LLM span (child of the HTTP request span from tracing.Middleware).
		// noop tracer is used when OTel is not initialised — no overhead.
		genAISystem := tracing.ChannelTypeToGenAISystem(channel.Type)
		_, llmSpan := tracing.StartLLMSpan(c.Request.Context(), genAISystem, relayInfo.OriginModelName)
		defer llmSpan.End()

		switch relayFormat {
		case types.RelayFormatOpenAIRealtime:
			newAPIError = relay.WssHelper(c, relayInfo)
		case types.RelayFormatClaude:
			newAPIError = relay.ClaudeHelper(c, relayInfo)
		case types.RelayFormatGemini:
			newAPIError = geminiRelayHandler(c, relayInfo)
		default:
			newAPIError = relayHandler(c, relayInfo)
		}

		// Record metrics
		relayDuration := time.Since(relayStart).Seconds()
		status := "success"
		if newAPIError != nil {
			status = "error"
		}
		metrics.RecordRelayRequest(providerName, relayInfo.OriginModelName, status, relayDuration)

		// Annotate LLM span with usage and status after relay completes.
		// costCNY is estimated from pre-consumed quota; final settlement happens async.
		{
			var relayErr error
			if newAPIError != nil {
				relayErr = newAPIError.Err
			}
			costCNY := float64(relayInfo.FinalPreConsumedQuota) / common.QuotaPerUnit
			tracing.SetGenAIAttributes(llmSpan,
				relayInfo.GetEstimatePromptTokens(), 0,
				costCNY, nil, relayErr)
		}

		// Trace this attempt on the request's log row: on success it names the
		// channel that actually served it, on failure it preserves why we moved on
		// (which is otherwise lost the moment the next attempt starts).
		{
			attempt := app.RouteAttempt{
				ChannelID:   channel.Id,
				ChannelName: channel.Name,
				Provider:    providerName,
				Outcome:     app.RouteAttemptOutcomeSuccess,
				DurationMs:  time.Since(relayStart).Milliseconds(),
			}
			if newAPIError != nil {
				attempt.Outcome = app.RouteAttemptOutcomeUpstreamErr
				attempt.ErrorCode = string(newAPIError.GetErrorCode())
				attempt.StatusCode = newAPIError.StatusCode
			}
			app.RecordRouteAttempt(c, attempt)
		}

		if newAPIError == nil {
			channelBreakers.RecordSuccess(channel.Id)
			hub.RecordRelayOutcome(channel.Id, true, relayDuration,
				c.GetString("tenant_id"),
				relayInfo.OriginModelName,
				int64(relayInfo.GetEstimatePromptTokens()), 0,
				int64(relayInfo.FinalPreConsumedQuota), 0)
			return
		}

		// Only count provider/network failures toward the breaker. 4xx user errors
		// and client cancellation must not trip a healthy channel.
		if types.IsUpstreamFailure(newAPIError) {
			channelBreakers.RecordFailure(channel.Id)
		}
		hub.RecordRelayOutcome(channel.Id, false, relayDuration,
			c.GetString("tenant_id"),
			relayInfo.OriginModelName, 0, 0, 0, 0)
		processChannelError(c, *types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, common.GetContextKeyString(c, constant.ContextKeyChannelKey), channel.GetAutoBan()), newAPIError)

		if !shouldRetry(c, newAPIError, common.RetryTimes-retryParam.GetRetry()) {
			break
		}

		// O2 (B3): we're about to retry on a fresh channel. Count it as a failover
		// when the cause was an upstream/provider failure (not a user 4xx) —
		// providerName is the channel we're failing OVER FROM. Separate series from
		// RetryAttempts (different name/labels), so no double-count in alerts.
		if types.IsUpstreamFailure(newAPIError) {
			metrics.RecordRelayFailover(providerName, "upstream_error")
		}

		// Record retry attempt
		metrics.RetryAttempts.WithLabelValues(providerName, string(newAPIError.GetErrorCode())).Inc()
	}

	useChannel := c.GetStringSlice("use_channel")
	if len(useChannel) > 1 {
		retryLogStr := fmt.Sprintf("重试：%s", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(useChannel)), "->"), "[]"))
		logger.LogInfo(c, retryLogStr)
	}
}

// returnPreConsumedQuota is a call seam — package-level var so hermetic tests can
// assert the failure-path release without a live platform endpoint (same seam
// convention as the wallet calls in tenant_credit_pool.go).
var returnPreConsumedQuota = app.ReturnPreConsumedQuota

// releasePreConsumedOnFailure hands the pre-consumption back when a relay attempt
// ended in error. FinalPreConsumedQuota is deliberately NOT part of the gate: the
// trust path zeroes it while the platform wallet hold stays live, so gating on it
// left that hold frozen until its TTL expired. Both halves of the hand-back are
// already guarded inside ReturnPreConsumedQuota — the local refund on
// FinalPreConsumedQuota != 0, the platform release on PlatformPreAuthID > 0 —
// so an error alone is the whole condition and nothing is refunded twice.
func releasePreConsumedOnFailure(c *gin.Context, apiErr *types.NewAPIError, relayInfo *relaycommon.RelayInfo) {
	if apiErr == nil {
		return
	}
	returnPreConsumedQuota(c, relayInfo)
}

var upgrader = websocket.Upgrader{
	Subprotocols: []string{"realtime"}, // WS 握手支持的协议，如果有使用 Sec-WebSocket-Protocol，则必须在此声明对应的 Protocol TODO add other protocol
	CheckOrigin: func(r *http.Request) bool {
		return true // 允许跨域
	},
}

func addUsedChannel(c *gin.Context, channelId int) {
	useChannel := c.GetStringSlice("use_channel")
	useChannel = append(useChannel, fmt.Sprintf("%d", channelId))
	c.Set("use_channel", useChannel)
}

func fastTokenCountMetaForPricing(request dto.Request) *types.TokenCountMeta {
	if request == nil {
		return &types.TokenCountMeta{}
	}
	meta := &types.TokenCountMeta{
		TokenType: types.TokenTypeTokenizer,
	}
	switch r := request.(type) {
	case *dto.GeneralOpenAIRequest:
		if r.MaxCompletionTokens > r.MaxTokens {
			meta.MaxTokens = int(r.MaxCompletionTokens)
		} else {
			meta.MaxTokens = int(r.MaxTokens)
		}
	case *dto.OpenAIResponsesRequest:
		meta.MaxTokens = int(r.MaxOutputTokens)
	case *dto.ClaudeRequest:
		meta.MaxTokens = int(r.MaxTokens)
	case *dto.ImageRequest:
		// Pricing for image requests depends on ImagePriceRatio; safe to compute even when CountToken is disabled.
		return r.GetTokenCountMeta()
	default:
		// Best-effort: leave CombineText empty to avoid large allocations.
	}
	return meta
}

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
	channel, selectGroup, err := app.CacheGetRandomSatisfiedChannel(retryParam)

	info.PriceData.GroupRatioInfo = helper.HandleGroupRatio(c, info)

	if err != nil {
		return nil, types.NewError(fmt.Errorf("获取分组 %s 下模型 %s 的可用渠道失败（retry）: %s", selectGroup, info.OriginModelName, err.Error()), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	if channel == nil {
		return nil, types.NewError(fmt.Errorf("分组 %s 下模型 %s 的可用渠道不存在（retry）", selectGroup, info.OriginModelName), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}

	newAPIError := middleware.SetupContextForSelectedChannel(c, channel, info.OriginModelName)
	if newAPIError != nil {
		return nil, newAPIError
	}
	return channel, nil
}

func shouldRetry(c *gin.Context, openaiErr *types.NewAPIError, retryTimes int) bool {
	if openaiErr == nil {
		return false
	}
	// Streaming safety gate — MUST stay ahead of every other rule, including the
	// unconditional channel-error retry below.
	//
	// The retry loop in RelayHandler hands the SAME gin.ResponseWriter to each
	// attempt. Once any byte has been flushed, a retry appends a second complete
	// response to the client's stream: the consumer sees duplicated/interleaved
	// SSE frames it cannot un-mix, and the user is billed for both attempts.
	// The partial output has already left the process, so no upstream can resume
	// it — the only correct move is to stop and let the caller's error path
	// surface the failure in-band (see the c.Writer.Written() branch in
	// RelayHandler's deferred error renderer).
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
		return true
	}
	if openaiErr.StatusCode == 307 {
		return true
	}
	if openaiErr.StatusCode/100 == 5 {
		// 超时不重试
		if openaiErr.StatusCode == 504 || openaiErr.StatusCode == 524 {
			return false
		}
		return true
	}
	if openaiErr.StatusCode == http.StatusBadRequest {
		return false
	}
	if openaiErr.StatusCode == 408 {
		// azure处理超时不重试
		return false
	}
	if openaiErr.StatusCode/100 == 2 {
		return false
	}
	return true
}

func processChannelError(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError) {
	logger.LogError(c, fmt.Sprintf("channel error (channel #%d, status code: %d): %s", channelError.ChannelId, err.StatusCode, err.Error()))
	// 不要使用context获取渠道信息，异步处理时可能会出现渠道信息不一致的情况
	// do not use context to get channel info, there may be inconsistent channel info when processing asynchronously
	if app.ShouldDisableChannel(channelError.ChannelType, err) && channelError.AutoBan {
		gopool.Go(func() {
			app.DisableChannel(channelError, err.Error())
		})
	}

	// OpenRouter free-key pool: rate-limited keys get a per-key cooldown rather
	// than being treated as permanently disabled. No-op for non-OpenRouter or non-429.
	gopool.Go(func() {
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
	if upModel := c.GetString("original_model"); upModel != "" {
		other["upstream_model"] = upModel
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

func RelayMidjourney(c *gin.Context) {
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatMjProxy, nil, nil)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"description": fmt.Sprintf("failed to generate relay info: %s", err.Error()),
			"type":        "upstream_error",
			"code":        4,
		})
		return
	}

	var mjErr *dto.MidjourneyResponse
	switch relayInfo.RelayMode {
	case relayconstant.RelayModeMidjourneyNotify:
		mjErr = relay.RelayMidjourneyNotify(c)
	case relayconstant.RelayModeMidjourneyTaskFetch, relayconstant.RelayModeMidjourneyTaskFetchByCondition:
		mjErr = relay.RelayMidjourneyTask(c, relayInfo.RelayMode)
	case relayconstant.RelayModeMidjourneyTaskImageSeed:
		mjErr = relay.RelayMidjourneyTaskImageSeed(c)
	case relayconstant.RelayModeSwapFace:
		mjErr = relay.RelaySwapFace(c, relayInfo)
	default:
		mjErr = relay.RelayMidjourneySubmit(c, relayInfo)
	}
	//err = relayMidjourneySubmit(c, relayMode)
	log.Println(mjErr)
	if mjErr != nil {
		statusCode := http.StatusBadRequest
		if mjErr.Code == 30 {
			mjErr.Result = "当前分组负载已饱和，请稍后再试，或升级账户以提升服务质量。"
			statusCode = http.StatusTooManyRequests
		}
		c.JSON(statusCode, gin.H{
			"description": fmt.Sprintf("%s %s", mjErr.Description, mjErr.Result),
			"type":        "upstream_error",
			"code":        mjErr.Code,
		})
		channelId := c.GetInt("channel_id")
		logger.LogError(c, fmt.Sprintf("relay error (channel #%d, status code %d): %s", channelId, statusCode, fmt.Sprintf("%s %s", mjErr.Description, mjErr.Result)))
	}
}

func RelayNotImplemented(c *gin.Context) {
	err := types.OpenAIError{
		Message: "API not implemented",
		Type:    "new_api_error",
		Param:   "",
		Code:    "api_not_implemented",
	}
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": err,
	})
}

func RelayNotFound(c *gin.Context) {
	err := types.OpenAIError{
		Message: fmt.Sprintf("Invalid URL (%s %s)", c.Request.Method, c.Request.URL.Path),
		Type:    "invalid_request_error",
		Param:   "",
		Code:    "",
	}
	c.JSON(http.StatusNotFound, gin.H{
		"error": err,
	})
}

func RelayTask(c *gin.Context) {
	retryTimes := common.RetryTimes
	channelId := c.GetInt("channel_id")
	c.Set("use_channel", []string{fmt.Sprintf("%d", channelId)})
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
	if err != nil {
		return
	}
	taskErr := taskRelayHandler(c, relayInfo)
	if taskErr == nil {
		retryTimes = 0
	}
	retryParam := &app.RetryParam{
		Ctx:        c,
		TokenGroup: relayInfo.TokenGroup,
		ModelName:  relayInfo.OriginModelName,
		Retry:      common.GetPointer(0),
	}
	for ; shouldRetryTaskRelay(c, channelId, taskErr, retryTimes) && retryParam.GetRetry() < retryTimes; retryParam.IncreaseRetry() {
		channel, newAPIError := getChannel(c, relayInfo, retryParam)
		if newAPIError != nil {
			logger.LogError(c, fmt.Sprintf("CacheGetRandomSatisfiedChannel failed: %s", newAPIError.Error()))
			taskErr = app.TaskErrorWrapperLocal(newAPIError.Err, "get_channel_failed", http.StatusInternalServerError)
			break
		}
		channelId = channel.Id
		useChannel := c.GetStringSlice("use_channel")
		useChannel = append(useChannel, fmt.Sprintf("%d", channelId))
		c.Set("use_channel", useChannel)
		logger.LogInfo(c, fmt.Sprintf("using channel #%d to retry (remain times %d)", channel.Id, retryParam.GetRetry()))
		//middleware.SetupContextForSelectedChannel(c, channel, originalModel)

		requestBody, err := common.GetRequestBody(c)
		if err != nil {
			if common.IsRequestBodyTooLargeError(err) || errors.Is(err, common.ErrRequestBodyTooLarge) {
				taskErr = app.TaskErrorWrapperLocal(err, "read_request_body_failed", http.StatusRequestEntityTooLarge)
			} else {
				taskErr = app.TaskErrorWrapperLocal(err, "read_request_body_failed", http.StatusBadRequest)
			}
			break
		}
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		taskErr = taskRelayHandler(c, relayInfo)
	}
	useChannel := c.GetStringSlice("use_channel")
	if len(useChannel) > 1 {
		retryLogStr := fmt.Sprintf("重试：%s", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(useChannel)), "->"), "[]"))
		logger.LogInfo(c, retryLogStr)
	}
	if taskErr != nil {
		if taskErr.StatusCode == http.StatusTooManyRequests {
			taskErr.Message = "当前分组上游负载已饱和，请稍后再试"
		}
		c.JSON(taskErr.StatusCode, taskErr)
	}

	// Record video model health metrics for the video-status endpoint.
	if isVideoModel(relayInfo.OriginModelName) {
		if taskErr != nil {
			RecordVideoModelFailure(relayInfo.OriginModelName, taskErr.Message)
		} else {
			RecordVideoModelSuccess(relayInfo.OriginModelName, 0)
		}
	}
}

func taskRelayHandler(c *gin.Context, relayInfo *relaycommon.RelayInfo) *dto.TaskError {
	var err *dto.TaskError
	switch relayInfo.RelayMode {
	case relayconstant.RelayModeSunoFetch, relayconstant.RelayModeSunoFetchByID, relayconstant.RelayModeVideoFetchByID, relayconstant.RelayModeMusicFetchByID:
		err = relay.RelayTaskFetch(c, relayInfo.RelayMode)
	default:
		err = relay.RelayTaskSubmit(c, relayInfo)
	}
	return err
}

func shouldRetryTaskRelay(c *gin.Context, channelId int, taskErr *dto.TaskError, retryTimes int) bool {
	if taskErr == nil {
		return false
	}
	// Same streaming-safety gate as shouldRetry: the async-task relay shares the
	// single-ResponseWriter retry loop, so a retry after a partial write would
	// concatenate a second body onto the client's response.
	if c != nil && c.Writer != nil && c.Writer.Written() {
		metrics.RecordFailoverSuppressed("stream_already_started")
		logger.LogDebug(c, "task response already written (%d bytes); suppressing failover", c.Writer.Size())
		return false
	}
	if retryTimes <= 0 {
		return false
	}
	if _, ok := c.Get("specific_channel_id"); ok {
		return false
	}
	if taskErr.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if taskErr.StatusCode == 307 {
		return true
	}
	if taskErr.StatusCode/100 == 5 {
		// 超时不重试
		if taskErr.StatusCode == 504 || taskErr.StatusCode == 524 {
			return false
		}
		return true
	}
	if taskErr.StatusCode == http.StatusBadRequest {
		return false
	}
	if taskErr.StatusCode == 408 {
		// azure处理超时不重试
		return false
	}
	if taskErr.LocalError {
		return false
	}
	if taskErr.StatusCode/100 == 2 {
		return false
	}
	return true
}
