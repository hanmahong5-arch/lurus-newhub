package handler

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/relay"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/currency"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/resilience"
	"github.com/LurusTech/lurus-hub/internal/pkg/tracing"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

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

var returnPreConsumedQuota = app.ReturnPreConsumedQuota

var upgrader = websocket.Upgrader{
	Subprotocols: []string{"realtime"},
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func relayHandler(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	switch info.RelayMode {
	case relayconstant.RelayModeImagesGenerations, relayconstant.RelayModeImagesEdits:
		return relay.ImageHelper(c, info)
	case relayconstant.RelayModeAudioSpeech, relayconstant.RelayModeAudioTranslation, relayconstant.RelayModeAudioTranscription:
		return relay.AudioHelper(c, info)
	case relayconstant.RelayModeRerank:
		return relay.RerankHelper(c, info)
	case relayconstant.RelayModeSystemOne:
		return relay.SystemOneHelper(c, info)
	case relayconstant.RelayModeEmbeddings:
		return relay.EmbeddingHelper(c, info)
	case relayconstant.RelayModeResponses, relayconstant.RelayModeResponsesCompact:
		return relay.ResponsesHelper(c, info)
	default:
		return relay.TextHelper(c, info)
	}
}

func geminiRelayHandler(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	if strings.Contains(c.Request.URL.Path, "embed") {
		return relay.GeminiEmbeddingHandler(c, info)
	}
	return relay.GeminiHelper(c, info)
}

func executeChannelRelayAttempt(c *gin.Context, channel *repo.Channel, relayFormat types.RelayFormat, relayInfo *relaycommon.RelayInfo) (*types.NewAPIError, float64) {
	relayStart := time.Now()
	providerName := constant.GetChannelTypeName(channel.Type)

	genAISystem := tracing.ChannelTypeToGenAISystem(channel.Type)
	_, llmSpan := tracing.StartLLMSpan(c.Request.Context(), genAISystem, relayInfo.OriginModelName)
	defer llmSpan.End()

	var newAPIError *types.NewAPIError
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

	relayDuration := time.Since(relayStart).Seconds()

	var relayErr error
	if newAPIError != nil {
		relayErr = newAPIError.Err
	}
	costCNY := currency.QuotaToCNY(relayInfo.FinalPreConsumedQuota)
	tracing.SetGenAIAttributes(llmSpan, relayInfo.GetEstimatePromptTokens(), 0, costCNY, nil, relayErr)

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

	return newAPIError, relayDuration
}

func releasePreConsumedOnFailure(c *gin.Context, apiErr *types.NewAPIError, relayInfo *relaycommon.RelayInfo) {
	if apiErr == nil {
		return
	}
	returnPreConsumedQuota(c, relayInfo)
}
