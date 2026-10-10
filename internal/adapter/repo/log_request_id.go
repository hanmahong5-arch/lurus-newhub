package repo

import (
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// setRequestIdIfAbsent stamps other["request_id"] from the request-scoped id
// (middleware.RequestId, read via common.RequestIdKey) when the caller
// hasn't already put one there. Shared by RecordConsumeLog and
// RecordErrorLog so both the success and error rows carry it (relay.go's
// error path and utils.go's abort helper build their own `other` maps
// upstream of here — this is the single place both funnel through).
func setRequestIdIfAbsent(c *gin.Context, other map[string]interface{}) map[string]interface{} {
	if other == nil {
		other = make(map[string]interface{})
	}
	if _, exists := other["request_id"]; exists {
		return other
	}
	if reqId := c.GetString(common.RequestIdKey); reqId != "" {
		other["request_id"] = reqId
	}
	return other
}

// bodyRequestID is the id the body archive is addressed by: the same request
// id the log row carries in logs.other, so an operator can go from a log line
// to its body.
func bodyRequestID(c *gin.Context, other map[string]interface{}) string {
	if s, ok := other["request_id"].(string); ok && s != "" {
		return s
	}
	return c.GetString(common.RequestIdKey)
}
