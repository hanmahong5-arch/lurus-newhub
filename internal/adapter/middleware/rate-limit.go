package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
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

// rateLimitMemoryFallbackMarks are the buckets that guard a credential or an
// account-creation path rather than traffic volume. When their Redis backend
// errors they degrade to the process-local limiter (a real ceiling, per
// replica) instead of to cycle-11's fail-open (no ceiling at all). Each
// entry names what it is guarding; TestRateLimitMarks_EveryMarkIsClassified
// walks every non-test Go file under internal/ and requires every mark it
// finds to be in this table or in the exempt table beside it.
//
// The value is the reason, not a description: it is the thing to argue with
// when reclassifying.
var rateLimitMemoryFallbackMarks = map[string]string{
	"RD":     "RedemptionRateLimit: redemption-code guessing is a money path, and an unthrottled guessing run against a code space is exactly what this bucket exists for.",
	"BS":     "BootstrapRateLimit: a leaked platform SDK cookie replayed from many client identities auto-creates newhub users; unthrottled, the blast radius is unbounded.",
	"TB":     "TotpBackupCodesRateLimit: backup-code regeneration invalidates the old set and mints a new one — a second-factor credential path.",
	"CT":     "CriticalRateLimit: channel-key reveal and TOTP disable share this bucket; both hand out or remove a credential.",
	"PG":     "PlanGrantRateLimit: credits plan quota onto a buyer's balance — a money path; idempotent per period, but each call still verifies a JWT and hits the DB.",
	"TU":     "TopupRateLimit: wallet-to-quota transfer. Deliberately unmounted today (see TopupRateLimit), classified here so re-opening that money path does not silently re-open it unthrottled.",
	"IKP-IP": "InternalApiRateLimit pre-auth IP tier: mounted BEFORE InternalApiAuth precisely to bound invalid-key floods, i.e. internal-API-key guessing. Unlike the per-key tiers it cannot be keyed to an authenticated caller.",
}

// rateLimitFallsBackToMemory reports whether this bucket degrades to the
// process-local limiter on a Redis error.
func rateLimitFallsBackToMemory(mark string) bool {
	_, ok := rateLimitMemoryFallbackMarks[mark]
	return ok
}

func redisRateLimiterKeyed(c *gin.Context, maxRequestNum int, duration int64, mark, ident string) {
	ctx := c.Request.Context()
	rdb := common.RDB
	key := "rateLimit:" + mark + ident
	listLength, err := rdb.LLen(ctx, key).Result()
	if err != nil {
		// Fail OPEN, not closed (cycle-11 L2 / operator decision D1's web/API
		// half): this used to 500+Abort every request behind
		// GlobalAPIRateLimit/GlobalWebRateLimit, including the k8s probe
		// paths mounted behind GlobalAPIRateLimit — a Redis blip took every
		// replica out of Service readiness at once. Matches the relay-path
		// limiters' existing fail-open contract (model-rate-limit.go,
		// business_rate_limit.go, concurrency_limit.go): a Redis hiccup must
		// not become an outage of its own.
		common.SysError("redis rate limit LLen error: " + err.Error())
		if rateLimitFallsBackToMemory(mark) {
			// Credential/abuse bucket: degrade to the process-local limiter
			// rather than to no limiter. Per replica, so the effective
			// cluster budget is replicas x budget — weaker than Redis, and
			// not the same thing as unthrottled. inMemoryRateLimiter was
			// initialised by the factory that built this middleware (see
			// rateLimitFactory / keyedRateLimitFactory); Init is not called
			// here because it writes a package-global under a check that is
			// not itself synchronised, which on a request path would be a
			// data race.
			metrics.RecordRateLimitDegraded("web_rate_limit_backend_memory")
			r6aRateLimitDegradedLogf("web_rate_limit_backend_memory",
				"web/API rate limit LLen error on credential bucket "+mark+", falling back to the in-process limiter: "+err.Error())
			memoryRateLimiterKeyed(c, maxRequestNum, duration, mark, ident)
			return
		}
		metrics.RecordRateLimitDegraded("web_rate_limit_backend")
		r6aRateLimitDegradedLogf("web_rate_limit_backend", "web/API rate limit LLen error, failing open: "+err.Error())
		c.Next()
		return
	}
	if listLength < int64(maxRequestNum) {
		rdb.LPush(ctx, key, time.Now().Format(timeFormat))
		rdb.Expire(ctx, key, common.RateLimitKeyExpirationDuration)
	} else {
		oldTimeStr, _ := rdb.LIndex(ctx, key, -1).Result()
		oldTime, err := time.Parse(timeFormat, oldTimeStr)
		if err != nil {
			// Corrupt stored value, not a backend outage: the key itself is
			// unusable, so self-heal by deleting it — a fresh window starts
			// on the next request — instead of every future request for
			// this ident failing the same parse forever.
			common.SysError("redis rate limit time parse error: " + err.Error())
			metrics.RecordRateLimitDegraded("web_rate_limit_corrupt")
			r6aRateLimitDegradedLogf("web_rate_limit_corrupt", "web/API rate limit corrupt timestamp, failing open: "+err.Error())
			rdb.Del(ctx, key)
			c.Next()
			return
		}
		nowTimeStr := time.Now().Format(timeFormat)
		nowTime, err := time.Parse(timeFormat, nowTimeStr)
		if err != nil {
			// Same corrupt-value contract as above; this branch parses a
			// timestamp newhub just formatted itself, so it is effectively
			// unreachable in practice, but the handling stays symmetric.
			common.SysError("redis rate limit time parse error: " + err.Error())
			metrics.RecordRateLimitDegraded("web_rate_limit_corrupt")
			r6aRateLimitDegradedLogf("web_rate_limit_corrupt", "web/API rate limit corrupt timestamp, failing open: "+err.Error())
			rdb.Del(ctx, key)
			c.Next()
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
		if rateLimitFallsBackToMemory(mark) {
			// Arm the process-local limiter now, at route-registration time:
			// redisRateLimiterKeyed's fallback needs its store, and Init
			// cannot be called from a request path without racing.
			inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
		}
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

// probeBypassPaths are the k8s liveness/readiness probe routes: a probe
// failure restarts the pod, so these are exempted from GlobalAPIRateLimit
// when the request is a direct in-cluster hit (loopback/private RemoteAddr
// with none of the three forwarding headers present — see
// IsDirectInClusterRequest in direct_request.go). In today's topology that
// bypass condition is met by the kubelet hitting the pod's NodePort
// directly, and also by anything else calling the pod from inside the node
// or cluster network with no forwarding header. A request relayed through
// the host nginx carries a forwarding header (deploy/r6-host-nginx/*.conf)
// and so is not classified as direct, and stays rate limited exactly as
// before — /api/health through nginx still performs a real DB ping and is
// worth protecting.
var probeBypassPaths = map[string]bool{
	"/api/status": true,
	"/api/health": true,
}

func GlobalAPIRateLimit() func(c *gin.Context) {
	next := defNext
	if common.GlobalApiRateLimitEnable {
		next = rateLimitFactory(common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration, "GA")
	}
	return func(c *gin.Context) {
		if probeBypassPaths[c.FullPath()] && IsDirectInClusterRequest(c) {
			c.Next()
			return
		}
		next(c)
	}
}

func CriticalRateLimit() func(c *gin.Context) {
	if common.CriticalRateLimitEnable {
		return rateLimitFactory(common.CriticalRateLimitNum, common.CriticalRateLimitDuration, "CT")
	}
	return defNext
}

// TotpBackupCodesRateLimit limits backup-code regeneration to 5 per 20
// minutes per IP — its own "TB" bucket, deliberately separate from
// CriticalRateLimit's shared "CT" bucket (channel-key reveal, TOTP disable)
// so a burst against one endpoint cannot throttle a caller out of the other
// (§8 L6 amendment: "not CriticalRateLimit()'s IP-keyed 'CT' bucket").
func TotpBackupCodesRateLimit() func(c *gin.Context) {
	return rateLimitFactory(5, 20*60, "TB")
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
//
// Deliberately unmounted (cycle-8 L1 operator decision): its intended
// consumer is POST /api/v2/:tenant_slug/billing/topup (TopUpV2), which
// api-v2-router.go leaves unrouted on purpose — see the comment beside
// tenantBilling.GET("/topups", ...) there. Re-opening that money path,
// and deciding whether this limiter guards it, is an owner's call.
func TopupRateLimit() func(c *gin.Context) {
	return rateLimitFactory(5, 60, "TU")
}

// PlanGrantRateLimit caps POST /api/v2/:slug/plan-grant to 10/min/IP. It has
// its own bucket on purpose: sharing BootstrapRateLimit's 5/min would make
// every Switch login (provision + plan-grant) spend two of the five slots an
// office NAT shares, and the grant is idempotent, so a tighter shared cap
// buys nothing.
func PlanGrantRateLimit() func(c *gin.Context) {
	return rateLimitFactory(10, 60, "PG")
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
		if rateLimitFallsBackToMemory(mark) {
			// Same reason as rateLimitFactory's: arm the fallback store here,
			// not on the request path.
			inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
		}
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
