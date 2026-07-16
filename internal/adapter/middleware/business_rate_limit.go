package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// Business rate limiting — per-token and per-tenant RPM + TPM sliding windows.
//
// Unlike middleware/rate-limit.go (ClientIP dimension) this enforces the
// enterprise quota dimensions: the authenticated token and its owning tenant.
// Limits come from tokens.rpm_limit/tpm_limit and tenants.rpm_limit/tpm_limit
// (migration 023); 0 = unlimited, so every pre-023 row keeps today's behavior
// unchanged.
//
// RPM backend: Redis ZSET sliding window (score = admission unix-milli) when
// common.RedisEnabled, else an in-process sliding window (single-node
// semantics — fine for the Redis-less dev/test tier). Both fail OPEN on
// backend errors, matching CostSpikeLimit / PoolBalanceCheck: never block
// legitimate traffic on infrastructure hiccups.
//
// TPM is enforced from the SETTLED-usage windows owned by package app
// (internal/app/business_tpm.go, same dual backend + fail-open contract):
// PostConsumeQuota records each settlement via app.RecordBusinessTPMUsage and
// this middleware only READS them (app.QueryBusinessTPM*Window). The window
// store cannot live here: settled usage never reaches the gin context after
// c.Next() (it surfaces on the settlement path inside package app), and app
// importing middleware would be an import cycle since middleware already
// imports app (cost_spike.go, distributor.go) — so the store sits in app,
// mirroring how cost_spike splits its app-side window from its middleware
// reader. Admission semantics are documented on bizTPMAdmit.
//
// Position in chain: AFTER TokenAuth (needs the "token_id" context key),
// BEFORE Distribute (no point selecting a channel for a rejected request).

const (
	bizRateLimitWindow = time.Minute
	bizTokenKeyPrefix  = "rl:biz:tok:"
	bizTenantKeyPrefix = "rl:biz:tenant:"
	// bizRateLimitErrorCode is the machine-readable code in the relay-format
	// 429 body, distinguishable from the IP/model limiters' responses.
	bizRateLimitErrorCode = "business_rate_limit_exceeded"
)

// bizRateLimits holds the effective limits for one enforcement scope.
// 0 means unlimited for that dimension.
type bizRateLimits struct {
	RPM int
	TPM int
}

// Seams for hermetic tests: production wiring reads the DB and the wall
// clock; tests swap these to drive limits and window sliding deterministically.
var (
	fetchTokenBizLimits  = fetchTokenBizLimitsFromDB
	fetchTenantBizLimits = fetchTenantBizLimitsFromDB
	bizNow               = time.Now
)

// fetchTokenBizLimitsFromDB reads the token's rate-limit columns plus its
// tenant id in one primary-key SELECT. The Redis token cache (repo.Token)
// does not carry the 023 columns, so this goes straight to the DB — same
// per-request weight class as PoolBalanceCheck's pool lookup.
func fetchTokenBizLimitsFromDB(tokenID int) (bizRateLimits, string, error) {
	if repo.DB == nil {
		return bizRateLimits{}, "", errors.New("db not initialized")
	}
	var row struct {
		TenantId string
		RpmLimit int
		TpmLimit int
	}
	err := repo.DB.Model(&entity.Token{}).
		Select("tenant_id", "rpm_limit", "tpm_limit").
		Where("id = ?", tokenID).
		Take(&row).Error
	if err != nil {
		return bizRateLimits{}, "", err
	}
	return bizRateLimits{RPM: row.RpmLimit, TPM: row.TpmLimit}, row.TenantId, nil
}

// fetchTenantBizLimitsFromDB reads the tenant's rate-limit columns. A missing
// tenant row (e.g. the bootstrap "default" tenant on deployments that predate
// the tenants table) means no tenant-level limit — not an error.
func fetchTenantBizLimitsFromDB(tenantID string) (bizRateLimits, error) {
	if repo.DB == nil {
		return bizRateLimits{}, errors.New("db not initialized")
	}
	var row struct {
		RpmLimit int
		TpmLimit int
	}
	err := repo.DB.Model(&entity.Tenant{}).
		Select("rpm_limit", "tpm_limit").
		Where("id = ?", tenantID).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return bizRateLimits{}, nil
	}
	if err != nil {
		return bizRateLimits{}, err
	}
	return bizRateLimits{RPM: row.RpmLimit, TPM: row.TpmLimit}, nil
}

// bizRetryAfterSec computes the whole seconds until the oldest in-window
// admission slides out, clamped to at least 1 so a 429 never says "retry now".
func bizRetryAfterSec(oldestMs, nowMs int64, window time.Duration) int64 {
	remaining := oldestMs + window.Milliseconds() - nowMs
	sec := (remaining + 999) / 1000
	if sec < 1 {
		sec = 1
	}
	return sec
}

// ---------------------------------------------------------------------------
// In-memory fallback (Redis disabled): per-process sliding window, pruned on
// access — no background goroutine, so tests and shutdown never leak.
// ---------------------------------------------------------------------------

type bizMemoryLimiter struct {
	mu      sync.Mutex
	entries map[string][]int64 // admission times (unix milli), ascending
}

var bizMemLimiter = bizMemoryLimiter{entries: make(map[string][]int64)}

// allow admits or denies one request for key under limit within window.
// On denial it returns the Retry-After seconds derived from the oldest
// in-window admission. Memory stays bounded by the set of active keys:
// out-of-window timestamps are pruned on every touch.
func (l *bizMemoryLimiter) allow(key string, limit int, window time.Duration, now time.Time) (bool, int64) {
	nowMs := now.UnixMilli()
	cutoff := nowMs - window.Milliseconds()

	l.mu.Lock()
	defer l.mu.Unlock()

	ts := l.entries[key]
	i := 0
	for i < len(ts) && ts[i] <= cutoff {
		i++
	}
	ts = ts[i:]
	if len(ts) >= limit {
		l.entries[key] = ts
		return false, bizRetryAfterSec(ts[0], nowMs, window)
	}
	l.entries[key] = append(ts, nowMs)
	return true, 0
}

// ---------------------------------------------------------------------------
// Redis backend: ZSET per key, score/member anchored on admission time.
// ---------------------------------------------------------------------------

// bizMemberSeq disambiguates ZSET members created in the same nanosecond
// across concurrent requests (ZADD on an identical member would silently
// collapse two admissions into one).
var bizMemberSeq atomic.Uint64

// bizRedisAllow implements the same admit/deny contract as bizMemoryLimiter
// against Redis. A non-nil error means "backend unusable" — the caller fails
// open. The check-then-add sequence is not atomic; a concurrent burst can
// briefly over-admit, the same accepted trade-off as app.QueryCostSpikeWindow.
func bizRedisAllow(ctx context.Context, rdb *redis.Client, key string, limit int, window time.Duration, now time.Time) (bool, int64, error) {
	nowMs := now.UnixMilli()
	cutoff := strconv.FormatInt(nowMs-window.Milliseconds(), 10)

	if err := rdb.ZRemRangeByScore(ctx, key, "-inf", cutoff).Err(); err != nil {
		return false, 0, fmt.Errorf("biz rate limit zremrangebyscore: %w", err)
	}
	count, err := rdb.ZCard(ctx, key).Result()
	if err != nil {
		return false, 0, fmt.Errorf("biz rate limit zcard: %w", err)
	}
	if count >= int64(limit) {
		oldest, err := rdb.ZRangeWithScores(ctx, key, 0, 0).Result()
		if err != nil {
			return false, 0, fmt.Errorf("biz rate limit zrange: %w", err)
		}
		if len(oldest) == 0 {
			// Raced empty between ZCard and ZRange; be conservative.
			return false, 1, nil
		}
		return false, bizRetryAfterSec(int64(oldest[0].Score), nowMs, window), nil
	}

	member := strconv.FormatInt(now.UnixNano(), 10) + "-" + strconv.FormatUint(bizMemberSeq.Add(1), 10)
	pipe := rdb.Pipeline()
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(nowMs), Member: member})
	pipe.Expire(ctx, key, window+10*time.Second)
	if _, err := pipe.Exec(ctx); err != nil {
		// Admission already decided; a failed record only under-counts.
		return true, 0, fmt.Errorf("biz rate limit zadd: %w", err)
	}
	return true, 0, nil
}

// bizAllow routes to the Redis or in-memory window. Errors fail open (allow)
// with an ops-visible log line.
func bizAllow(c *gin.Context, key string, limit int) (bool, int64) {
	now := bizNow()
	if common.RedisEnabled && common.RDB != nil {
		allowed, retryAfter, err := bizRedisAllow(c.Request.Context(), common.RDB, key, limit, bizRateLimitWindow, now)
		if err != nil {
			common.SysError("business rate limit backend error, failing open: " + err.Error())
			return true, 0
		}
		return allowed, retryAfter
	}
	return bizMemLimiter.allow(key, limit, bizRateLimitWindow, now)
}

// bizReject emits the 429: Retry-After + X-RateLimit-* headers, the relay
// error-format JSON body, and the rate-limited counter (scope × limitType,
// limitType ∈ {"rpm","tpm"}).
func bizReject(c *gin.Context, scope, limitType string, limit int, retryAfter int64) {
	metrics.RecordRateLimited(scope, limitType)
	setRateLimitResponseHeaders(c, limit, 0, retryAfter)
	scopeLabel := "令牌"
	switch scope {
	case "tenant":
		scopeLabel = "租户"
	case "model":
		scopeLabel = "模型"
	}
	measure := "请求数"
	if limitType == "tpm" {
		measure = " token 用量"
	}
	abortWithOpenAiMessage(c, http.StatusTooManyRequests,
		fmt.Sprintf("%s每分钟%s已达上限（%d/min），请 %d 秒后重试", scopeLabel, measure, limit, retryAfter),
		bizRateLimitErrorCode)
}

// bizTPMAdmit decides tokens-per-minute admission for one scope from the
// settled-usage window (total/oldestMs/err as returned by the
// app.QueryBusinessTPM*Window readers).
//
// Semantics — throttle SUSTAINED overuse, allow single spikes:
//
//   - The window holds only SETTLED usage, recorded by PostConsumeQuota via
//     app.RecordBusinessTPMUsage; admission itself records nothing. A single
//     oversized request hitting a quiet window is therefore ADMITTED — its
//     usage lands in the window afterwards and throttles the requests that
//     follow it, which is the intended contract for a usage-side limit.
//   - Deny when windowTotal + promptEstimate > limit. The estimate is
//     constant.ContextKeyPromptTokens when an earlier stage counted it; at
//     this position in the chain it is normally not yet set (token counting
//     happens later in the relay pipeline), so it degrades to 0 and the
//     settled window total alone decides.
//   - Backend errors fail OPEN, exactly like the RPM path.
//
// Returns false when the request was rejected (429 already written).
func bizTPMAdmit(c *gin.Context, scope string, limit int, total, oldestMs int64, err error) bool {
	if err != nil {
		common.SysError("business tpm limit backend error, failing open: " + err.Error())
		return true
	}
	estimate := int64(common.GetContextKeyInt(c, constant.ContextKeyPromptTokens))
	if estimate < 0 {
		estimate = 0
	}
	if total+estimate <= int64(limit) {
		return true
	}
	// Retry-After: when the oldest in-window record slides out. With an empty
	// window (deny driven purely by an over-limit estimate) there is nothing
	// to wait for — clamp to the 1s minimum like bizRetryAfterSec does.
	retryAfter := int64(1)
	if oldestMs > 0 {
		retryAfter = bizRetryAfterSec(oldestMs, app.BizTPMNow().UnixMilli(), bizRateLimitWindow)
	}
	bizReject(c, scope, "tpm", limit, retryAfter)
	return false
}

// BusinessRateLimit enforces per-token then per-tenant RPM and TPM sliding
// windows on the relay chain. Requests without an authenticated token (no
// "token_id" in context) pass through untouched; limit-lookup failures fail
// open.
func BusinessRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenID := c.GetInt("token_id")
		if tokenID == 0 {
			c.Next()
			return
		}

		tokenLimits, tenantID, err := fetchTokenBizLimits(tokenID)
		if err != nil {
			common.SysError(fmt.Sprintf("business rate limit: token %d limit lookup failed, failing open: %s", tokenID, err.Error()))
			c.Next()
			return
		}
		// Stash the resolved tenant for BusinessModelRateLimit (which runs
		// after Distribute, further down the chain) so the model dimension
		// doesn't repeat this token→tenant SELECT.
		c.Set(bizTenantIDContextKey, tenantID)

		if tokenLimits.RPM > 0 {
			allowed, retryAfter := bizAllow(c, bizTokenKeyPrefix+strconv.Itoa(tokenID), tokenLimits.RPM)
			if !allowed {
				bizReject(c, "token", "rpm", tokenLimits.RPM, retryAfter)
				return
			}
		}
		if tokenLimits.TPM > 0 {
			total, oldestMs, qerr := app.QueryBusinessTPMTokenWindow(c.Request.Context(), tokenID)
			if !bizTPMAdmit(c, "token", tokenLimits.TPM, total, oldestMs, qerr) {
				return
			}
		}

		if tenantID != "" {
			tenantLimits, err := fetchTenantBizLimits(tenantID)
			if err != nil {
				common.SysError(fmt.Sprintf("business rate limit: tenant %s limit lookup failed, failing open: %s", tenantID, err.Error()))
			} else {
				if tenantLimits.RPM > 0 {
					allowed, retryAfter := bizAllow(c, bizTenantKeyPrefix+tenantID, tenantLimits.RPM)
					if !allowed {
						bizReject(c, "tenant", "rpm", tenantLimits.RPM, retryAfter)
						return
					}
				}
				if tenantLimits.TPM > 0 {
					total, oldestMs, qerr := app.QueryBusinessTPMTenantWindow(c.Request.Context(), tenantID)
					if !bizTPMAdmit(c, "tenant", tenantLimits.TPM, total, oldestMs, qerr) {
						return
					}
				}
			}
		}

		c.Next()
	}
}
