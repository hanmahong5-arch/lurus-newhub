package middleware

import (
	"fmt"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/gin-gonic/gin"
)

func abortWithOpenAiMessage(c *gin.Context, statusCode int, message string, code ...string) {
	codeStr := ""
	if len(code) > 0 {
		codeStr = code[0]
	}
	userId := c.GetInt("id")
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"message": common.MessageWithRequestId(message, c.GetString(common.RequestIdKey)),
			"type":    "new_api_error",
			"code":    codeStr,
		},
	})
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
