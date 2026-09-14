package middleware

// responses_state_rate_limit.go — the IP-keyed rate limiter for GET/DELETE
// /v1/responses/:response_id (cycle-8 L7 repair round, findings
// B-F1/B-F2/A-F4/A-F5).
//
// The lane originally mounted middleware.CriticalRateLimit() here on the
// stated precedent "same as modelsRouter". That precedent was wrong:
// modelsRouter (relay-router.go) mounts TokenAuth only, no rate limiter at
// all. CriticalRateLimit's "CT" bucket (20 requests/20 minutes/client IP) is
// shared with several /api/* console routes — TOTP disable, channel-key
// reveal, audit export/analytics (see api-router.go / api-v2-router.go) — so
// a server-side integration polling this /v1 route from one egress address
// could throttle an operator out of those console routes from the same NAT,
// and vice versa. It was also, before this fix, the first CriticalRateLimit
// mount on a /v1 data-plane route: CriticalRateLimit's own keyed reject
// sites (rate-limit.go's redisRateLimiterKeyed/memoryRateLimiterKeyed) write
// headers only — no body — which contradicted three standing documentation
// contracts asserting that /v1 gateway-issued 429s always carry a wire body
// with error.code (see the B-F2 doc fixes in docs/openapi/relay.json and
// doc/product-integration-guide.md).
//
// ResponsesStateRateLimit fixes both problems: its own "RS" bucket, sized
// for polling a handful of in-flight background responses (a minute's worth,
// not CriticalRateLimit's twenty), and a reject rendered through
// abortWithOpenAiMessage — the same OpenAI-wire envelope every other /v1
// 429 in this codebase uses — so a 429 here always carries error.code.

import (
	"fmt"
	"net/http"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

const (
	// responsesStateRateLimitNum/Duration: 30 requests per 60 seconds per
	// client IP — a minute's worth of polling a handful of in-flight
	// background responses, not CriticalRateLimit's twenty-minutes-worth
	// human-triggered budget.
	responsesStateRateLimitNum      = 30
	responsesStateRateLimitDuration = int64(60)
	// responsesStateRateLimitMark is this group's OWN bucket prefix —
	// deliberately not CriticalRateLimit's shared "CT" mark, so a burst
	// against this route cannot throttle a caller out of the console routes
	// sharing "CT" (or vice versa). Mirrors TotpBackupCodesRateLimit's "TB"
	// precedent (rate-limit.go) for the same reason.
	responsesStateRateLimitMark = "RS"
)

// ResponsesStateRateLimit bounds GET/DELETE /v1/responses/:response_id to
// responsesStateRateLimitNum requests per responsesStateRateLimitDuration
// seconds per client IP, in its own "RS" bucket, with the reject rendered
// through the OpenAI-wire error envelope (see file doc comment).
func ResponsesStateRateLimit() func(c *gin.Context) {
	if common.RedisEnabled {
		return func(c *gin.Context) {
			responsesStateRedisRateLimit(c)
		}
	}
	// Safe to call multiple times (rateLimitFactory's own convention).
	inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
	return func(c *gin.Context) {
		responsesStateMemoryRateLimit(c)
	}
}

// responsesStateReject writes the same header set every keyed limiter in
// this package writes (Retry-After + X-RateLimit-Limit/Remaining/Scope/Type)
// and then renders the OpenAI-wire error envelope via abortWithOpenAiMessage
// — the one behavioural difference from redisRateLimiterKeyed/
// memoryRateLimiterKeyed (rate-limit.go), whose keyed reject sites stop at
// the headers and never call abortWithOpenAiMessage.
func responsesStateReject(c *gin.Context, retryAfter int64) {
	setRateLimitResponseHeaders(c, responsesStateRateLimitNum, 0, retryAfter)
	c.Writer.Header().Set("X-RateLimit-Scope", "ip")
	c.Writer.Header().Set("X-RateLimit-Type", "requests")
	abortWithOpenAiMessage(c, http.StatusTooManyRequests,
		fmt.Sprintf("ip requests limit exceeded: %d per %d s (%s); retry after %d s",
			responsesStateRateLimitNum, responsesStateRateLimitDuration,
			string(types.ErrorCodeRequestRateLimitExceeded), retryAfter),
		string(types.ErrorCodeRequestRateLimitExceeded))
}

func responsesStateMemoryRateLimit(c *gin.Context) {
	key := responsesStateRateLimitMark + c.ClientIP()
	if !inMemoryRateLimiter.Request(key, responsesStateRateLimitNum, responsesStateRateLimitDuration) {
		responsesStateReject(c, responsesStateRateLimitDuration)
	}
}

// responsesStateRedisRateLimit mirrors redisRateLimiterKeyed's list-based
// sliding window (rate-limit.go) against this file's own "RS" bucket, but
// rejects through responsesStateReject instead of a bare c.Status(429).
func responsesStateRedisRateLimit(c *gin.Context) {
	ctx := c.Request.Context()
	rdb := common.RDB
	key := "rateLimit:" + responsesStateRateLimitMark + c.ClientIP()
	listLength, err := rdb.LLen(ctx, key).Result()
	if err != nil {
		common.SysError("responses-state rate limit LLen error: " + err.Error())
		c.Status(http.StatusInternalServerError)
		c.Abort()
		return
	}
	if listLength < int64(responsesStateRateLimitNum) {
		rdb.LPush(ctx, key, time.Now().Format(timeFormat))
		rdb.Expire(ctx, key, common.RateLimitKeyExpirationDuration)
		return
	}
	oldTimeStr, _ := rdb.LIndex(ctx, key, -1).Result()
	oldTime, err := time.Parse(timeFormat, oldTimeStr)
	if err != nil {
		common.SysError("responses-state rate limit time parse error: " + err.Error())
		c.Status(http.StatusInternalServerError)
		c.Abort()
		return
	}
	nowTime, err := time.Parse(timeFormat, time.Now().Format(timeFormat))
	if err != nil {
		common.SysError("responses-state rate limit time parse error: " + err.Error())
		c.Status(http.StatusInternalServerError)
		c.Abort()
		return
	}
	if int64(nowTime.Sub(oldTime).Seconds()) < responsesStateRateLimitDuration {
		rdb.Expire(ctx, key, common.RateLimitKeyExpirationDuration)
		retryAfter := responsesStateRateLimitDuration - int64(nowTime.Sub(oldTime).Seconds())
		responsesStateReject(c, retryAfter)
		return
	}
	rdb.LPush(ctx, key, time.Now().Format(timeFormat))
	rdb.LTrim(ctx, key, 0, int64(responsesStateRateLimitNum-1))
	rdb.Expire(ctx, key, common.RateLimitKeyExpirationDuration)
}
