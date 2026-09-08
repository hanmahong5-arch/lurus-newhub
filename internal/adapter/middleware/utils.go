package middleware

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
	"github.com/gin-gonic/gin"
)

func abortWithOpenAiMessage(c *gin.Context, statusCode int, message string, code ...string) {
	codeStr := ""
	if len(code) > 0 {
		codeStr = code[0]
	}
	userId := c.GetInt("id")
	// NewErrorWithStatusCode (ErrorTypeNewAPIError) rather than
	// WithOpenAIError: it lets ToOpenAIError/ToClaudeError derive the
	// wire-native "type" from the status code via types.WireErrorType
	// instead of stamping the literal "new_api_error" into every wire — see
	// renderRejection's godoc and WireErrorType's. Both wire converters read
	// this same NewAPIError, so the OpenAI and Claude envelopes now diverge
	// correctly on 402/503/529 (WireErrorType: billing_error vs
	// insufficient_quota on 402, overloaded_error vs api_error on 503/529)
	// instead of sharing one hardcoded type.
	apiErr := types.NewErrorWithStatusCode(
		errors.New(common.MessageWithRequestId(message, c.GetString(common.RequestIdKey))),
		types.ErrorCode(codeStr),
		statusCode,
	)
	renderRejection(c, apiErr)
	c.Abort()
	logger.LogError(c.Request.Context(), fmt.Sprintf("user %d | %s", userId, message))
	recordMiddlewareErrorLog(c, statusCode, message, codeStr)
}

// recordMiddlewareErrorLog gives middleware-stage rejections the same durable
// error-log row that relay-stage failures get from processChannelError.
// Requests killed here (distributor model-not-found/no-channel, group/model
// authz, request-parse failures) never reach the relay handler, so before
// this they left no queryable trace at all — an operator answering "why do
// this customer's calls fail" had nothing to look at.
//
// Two exclusions keep this write off the flood-prone paths:
//   - anonymous requests (no authenticated user id): a key-guessing scanner
//     must not be able to turn 401 spam into DB inserts;
//   - 429s: every rate limiter rejects through this helper, and its whole job
//     is to make over-limit traffic cheap — bursts are already visible in
//     metrics. (The token-quota 402 path opts out further upstream by not
//     calling abortWithOpenAiMessage at all — see auth.go.)
func recordMiddlewareErrorLog(c *gin.Context, statusCode int, message string, codeStr string) {
	if !constant.ErrorLogEnabled || statusCode == http.StatusTooManyRequests {
		return
	}
	userId := c.GetInt("id")
	if userId <= 0 {
		return
	}
	other := map[string]interface{}{
		"status_code": statusCode,
		"stage":       "middleware",
	}
	if codeStr != "" {
		other["error_code"] = codeStr
	}
	if c.Request != nil && c.Request.URL != nil {
		other["request_path"] = c.Request.URL.Path
	}
	// Cross-product attribution: same resolver as the relay paths, so a
	// middleware rejection (no channel, authz, parse) still lands under the
	// product that sent it in /logs?source_product= instead of the default.
	other["source_product"] = ratio_setting.ResolveSourceProduct(c.GetHeader(ratio_setting.SourceProductHeader))
	repo.RecordErrorLog(c, userId, c.GetInt("channel_id"), c.GetString("original_model"),
		c.GetString("token_name"), message, c.GetInt("token_id"), 0, false, c.GetString("group"), other)
}

func abortWithMidjourneyMessage(c *gin.Context, statusCode int, code int, description string) {
	c.JSON(statusCode, gin.H{
		"description": description,
		"type":        "new_api_error",
		"code":        code,
	})
	c.Abort()
	logger.LogError(c.Request.Context(), description)
}
