package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

var timeFormat = "2006-01-02T15:04:05.000Z"

var inMemoryRateLimiter common.InMemoryRateLimiter

var defNext = func(c *gin.Context) {
	c.Next()
}

func redisRateLimiter(c *gin.Context, maxRequestNum int, duration int64, mark string) {
	redisRateLimiterKeyed(c, maxRequestNum, duration, mark, c.ClientIP())
}

// rateLimitScopeForIdent derives the X-RateLimit-Scope value from the bucket
// identity a keyed limiter was called with. internalApiRateLimitKey prefixes
// an authenticated internal-API-key bucket with "ik:" (falling back to
// "ip:" + the client IP only when the auth middleware did not run); plain
// ClientIP-keyed callers (redisRateLimiter / memoryRateLimiter) pass the bare
// IP with no prefix at all. Both "ip:"-prefixed and unprefixed idents are the
// same scope: ip.
func rateLimitScopeForIdent(ident string) string {
	if strings.HasPrefix(ident, "ik:") {
		return "key"
	}
	return "ip"
}

func redisRateLimiterKeyed(c *gin.Context, maxRequestNum int, duration int64, mark, ident string) {
	ctx := c.Request.Context()
	rdb := common.RDB
	key := "rateLimit:" + mark + ident
	listLength, err := rdb.LLen(ctx, key).Result()
	if err != nil {
		common.SysError("redis rate limit LLen error: " + err.Error())
		c.Status(http.StatusInternalServerError)
		c.Abort()
		return
	}
	if listLength < int64(maxRequestNum) {
		rdb.LPush(ctx, key, time.Now().Format(timeFormat))
		rdb.Expire(ctx, key, common.RateLimitKeyExpirationDuration)
	} else {
		oldTimeStr, _ := rdb.LIndex(ctx, key, -1).Result()
		oldTime, err := time.Parse(timeFormat, oldTimeStr)
		if err != nil {
			common.SysError("redis rate limit time parse error: " + err.Error())
			c.Status(http.StatusInternalServerError)
			c.Abort()
			return
		}
		nowTimeStr := time.Now().Format(timeFormat)
		nowTime, err := time.Parse(timeFormat, nowTimeStr)
		if err != nil {
			common.SysError("redis rate limit time parse error: " + err.Error())
			c.Status(http.StatusInternalServerError)
			c.Abort()
			return
		}
		// time.Since will return negative number!
		// See: https://stackoverflow.com/questions/50970900/why-is-time-since-returning-negative-durations-on-windows
		if int64(nowTime.Sub(oldTime).Seconds()) < duration {
			rdb.Expire(ctx, key, common.RateLimitKeyExpirationDuration)
			retryAfter := duration - int64(nowTime.Sub(oldTime).Seconds())
			setRateLimitResponseHeaders(c, maxRequestNum, 0, retryAfter)
			c.Writer.Header().Set("X-RateLimit-Scope", rateLimitScopeForIdent(ident))
			c.Writer.Header().Set("X-RateLimit-Type", "requests")
			c.Status(http.StatusTooManyRequests)
			c.Abort()
			return
		} else {
			rdb.LPush(ctx, key, time.Now().Format(timeFormat))
			rdb.LTrim(ctx, key, 0, int64(maxRequestNum-1))
			rdb.Expire(ctx, key, common.RateLimitKeyExpirationDuration)
		}
	}
}

func memoryRateLimiter(c *gin.Context, maxRequestNum int, duration int64, mark string) {
	memoryRateLimiterKeyed(c, maxRequestNum, duration, mark, c.ClientIP())
}

func memoryRateLimiterKeyed(c *gin.Context, maxRequestNum int, duration int64, mark, ident string) {
	key := mark + ident
	if !inMemoryRateLimiter.Request(key, maxRequestNum, duration) {
		setRateLimitResponseHeaders(c, maxRequestNum, 0, duration)
		c.Writer.Header().Set("X-RateLimit-Scope", rateLimitScopeForIdent(ident))
		c.Writer.Header().Set("X-RateLimit-Type", "requests")
		c.Status(http.StatusTooManyRequests)
		c.Abort()
		return
	}
}

func rateLimitFactory(maxRequestNum int, duration int64, mark string) func(c *gin.Context) {
	if common.RedisEnabled {
		return func(c *gin.Context) {
			redisRateLimiter(c, maxRequestNum, duration, mark)
		}
	} else {
		// It's safe to call multi times.
		inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
		return func(c *gin.Context) {
			memoryRateLimiter(c, maxRequestNum, duration, mark)
		}
	}
}

func GlobalWebRateLimit() func(c *gin.Context) {
	if common.GlobalWebRateLimitEnable {
		return rateLimitFactory(common.GlobalWebRateLimitNum, common.GlobalWebRateLimitDuration, "GW")
	}
	return defNext
}

func GlobalAPIRateLimit() func(c *gin.Context) {
	if common.GlobalApiRateLimitEnable {
		return rateLimitFactory(common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration, "GA")
	}
	return defNext
}

func CriticalRateLimit() func(c *gin.Context) {
	if common.CriticalRateLimitEnable {
		return rateLimitFactory(common.CriticalRateLimitNum, common.CriticalRateLimitDuration, "CT")
	}
	return defNext
}

func DownloadRateLimit() func(c *gin.Context) {
	return rateLimitFactory(common.DownloadRateLimitNum, common.DownloadRateLimitDuration, "DW")
}

func UploadRateLimit() func(c *gin.Context) {
	return rateLimitFactory(common.UploadRateLimitNum, common.UploadRateLimitDuration, "UP")
}

// RedemptionRateLimit limits redemption attempts to 5 per minute per IP.
// Prevents brute-force enumeration of redemption codes.
func RedemptionRateLimit() func(c *gin.Context) {
	return rateLimitFactory(5, 60, "RD")
}

// TopupRateLimit limits wallet-to-quota transfer to 5 per minute per IP.
func TopupRateLimit() func(c *gin.Context) {
	return rateLimitFactory(5, 60, "TU")
}

// BootstrapRateLimit caps zita-bootstrap to 5/min/IP. A leaked SDK cookie
// would otherwise let an attacker spam-create newhub users by replaying it
// from many client identities; capping here bounds the auto-create blast
// radius without affecting normal first-login latency.
func BootstrapRateLimit() func(c *gin.Context) {
	return rateLimitFactory(5, 60, "BS")
}

// internalApiRateLimitKey derives the per-key bucket identity from the
// authenticated internal API key (set by InternalApiAuth). Keying on the key id
// — not the client IP — is the whole point of P0-3: a stolen key replayed from a
// botnet of source IPs still shares ONE bucket and cannot be used to spread a
// provision/DoS flood across IPs. Falls back to client IP only if the auth
// middleware (impossibly) did not run, so an unauthenticated caller can never
// share a bucket with an authenticated one.
func internalApiRateLimitKey(c *gin.Context) string {
	if id := c.GetInt("internal_api_key_id"); id > 0 {
		return "ik:" + strconv.Itoa(id)
	}
	return "ip:" + c.ClientIP()
}

// keyedRateLimitFactory builds a rate-limit middleware whose bucket key is
// derived per-request by keyFn, rather than the client IP. Mirrors
// rateLimitFactory's redis-or-memory selection.
func keyedRateLimitFactory(maxRequestNum int, duration int64, mark string, keyFn func(*gin.Context) string) func(c *gin.Context) {
	if common.RedisEnabled {
		return func(c *gin.Context) {
			redisRateLimiterKeyed(c, maxRequestNum, duration, mark, keyFn(c))
		}
	}
	inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
	return func(c *gin.Context) {
		memoryRateLimiterKeyed(c, maxRequestNum, duration, mark, keyFn(c))
	}
}

// InternalApiRateLimit returns a per-API-key rate limiter for /internal routes.
// Must be mounted AFTER InternalApiAuth so the key id is present in context.
// mark must be unique per bucket tier so the tiers don't share a Redis list.
func InternalApiRateLimit(maxRequestNum int, duration int64, mark string) func(c *gin.Context) {
	if !common.InternalApiRateLimitEnable {
		return defNext
	}
	return keyedRateLimitFactory(maxRequestNum, duration, mark, internalApiRateLimitKey)
}

func setRateLimitResponseHeaders(c *gin.Context, limit int, remaining int, retryAfterSec int64) {
	c.Writer.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfterSec))
	c.Writer.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limit))
	c.Writer.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
}

// setRateLimitHeadroomHeaders writes the self-pacing header set on an
// ADMITTED response (unlike setRateLimitResponseHeaders, which serves the
// 429 reject sites and always includes Retry-After). remaining is clamped at
// 0 so a request that squeaked in under a since-tightened limit never
// reports a negative headroom. No Retry-After: there is nothing to retry,
// the request already went through.
func setRateLimitHeadroomHeaders(c *gin.Context, scope, limitType string, limit int, remaining int64, resetUnix int64) {
	if remaining < 0 {
		remaining = 0
	}
	h := c.Writer.Header()
	h.Set("X-RateLimit-Limit", strconv.Itoa(limit))
	h.Set("X-RateLimit-Remaining", strconv.FormatInt(remaining, 10))
	h.Set("X-RateLimit-Reset", strconv.FormatInt(resetUnix, 10))
	h.Set("X-RateLimit-Scope", scope)
	h.Set("X-RateLimit-Type", limitType)
}

// rateLimitHeadroomHeaderNames is every header setRateLimitHeadroomHeaders can
// write. It is the single source both reject sites (bizReject / ccReject,
// which must not leave a stale admit-path Reset — or any other field — next
// to their own 429) and the relay's upstream-error path (ClearRateLimitHeadroomHeaders
// below) use to erase a prior snapshot, so the two can never drift apart.
var rateLimitHeadroomHeaderNames = []string{
	"X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset",
	"X-RateLimit-Scope", "X-RateLimit-Type",
}

// ClearRateLimitHeadroomHeaders deletes any X-RateLimit-* headers already
// written to the response writer. Exported for handler/relay.go: a response
// describing an upstream-originated failure must never carry a stale
// gateway admit-path snapshot (see setRateLimitHeadroomHeaders) as if it
// described that response — a client would misread it as the gateway
// itself rate-limiting a request that was actually admitted and failed at
// the provider.
func ClearRateLimitHeadroomHeaders(c *gin.Context) {
	h := c.Writer.Header()
	for _, name := range rateLimitHeadroomHeaderNames {
		h.Del(name)
	}
}
