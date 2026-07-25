package handler

import (
	"errors"
	"net/http"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app/relay"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// RelayClaudeCountTokens serves POST /v1/messages/count_tokens.
//
// Deliberately NOT routed through RelayHandler: that path pre-consumes quota,
// settles usage, writes a billing log row and retries across channels. None of
// that applies to a free, side-effect-free size query — running it through the
// metered path would bill callers for asking how much something costs.
//
// A single attempt, no failover: if the selected channel cannot answer, the
// caller gets the upstream's own status rather than the gateway silently
// shopping the question around and charging latency for it.
func RelayClaudeCountTokens(c *gin.Context) {
	request, err := helper.GetAndValidateRequest(c, types.RelayFormatClaude)
	if err != nil {
		status := http.StatusBadRequest
		if common.IsRequestBodyTooLargeError(err) || errors.Is(err, common.ErrRequestBodyTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeClaudeCountTokensError(c, types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, status, types.ErrOptionWithSkipRetry()))
		return
	}

	info, err := relaycommon.GenRelayInfo(c, types.RelayFormatClaude, request, nil)
	if err != nil {
		writeClaudeCountTokensError(c, types.NewError(err, types.ErrorCodeGenRelayInfoFailed, types.ErrOptionWithSkipRetry()))
		return
	}

	if apiErr := relay.ClaudeCountTokensHelper(c, info); apiErr != nil {
		logger.LogError(c, "count_tokens error: "+apiErr.Error())
		writeClaudeCountTokensError(c, apiErr)
	}
}

// writeClaudeCountTokensError renders in the Anthropic error shape, which is
// what an SDK calling this endpoint is prepared to parse.
func writeClaudeCountTokensError(c *gin.Context, apiErr *types.NewAPIError) {
	if c.Writer.Written() {
		return
	}
	status := apiErr.StatusCode
	if status <= 0 {
		status = http.StatusInternalServerError
	}
	c.JSON(status, gin.H{
		"type":  "error",
		"error": apiErr.ToClaudeError(),
	})
}
