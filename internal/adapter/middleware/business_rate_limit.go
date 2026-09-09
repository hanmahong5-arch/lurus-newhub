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
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

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
)

// bizRateLimitErrorCode is the machine-readable code in the relay-format 429
// body, distinguishable from the IP/model limiters' responses. Defined from
// types.ErrorCodeBusinessRateLimitExceeded (not re-declared as its own
// literal) so the two cannot drift: openapi_contract_lock_test.go's lock (e)
// cross-checks every ErrorCode literal in internal/pkg/types against
// relay.json's GatewayError.code enum, and this string must stay the same
// value that check compares.
var bizRateLimitErrorCode = string(types.ErrorCodeBusinessRateLimitExceeded)

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

// bizHeadroomEnabled gates the self-pacing headers written on ADMITTED
// responses (rejects always carry their headers — see bizReject). Read once
// at package init like the other GetEnvOrDefault* callers in this codebase;
// tests reassign it directly to flip the seam. Default on: the headers are
// additive and no known consumer reads them yet (contracts.md), so the safer
// default is to ship the information rather than withhold it.
var bizHeadroomEnabled = common.GetEnvOrDefaultBool("RATE_LIMIT_HEADERS_ENABLED", true)

// bizWindowState is the RPM window's occupancy at the moment of an admit/deny
// decision, as reported by the backend that made the call — it lets the
// caller compute a headroom snapshot without a second read against Redis or
// the in-memory map. Valid is false when the backend could not report a
// window position (e.g. a Redis pipeline error that still fails open): the
// caller must then skip the headroom header for that check rather than
// publish a number it never actually observed.
type bizWindowState struct {
	Valid    bool
	Used     int64
	OldestMs int64
}

// headroom converts a window position into a self-pacing snapshot for scope/
// limitType under limit. reset is the instant the oldest in-window admission
// ages out, mirroring bizRetryAfterSec's window arithmetic (ceil to whole
// seconds, so Reset never reports an instant that has already passed by
// truncating away a sub-second remainder) but expressed as a unix second
// instead of a relative delay.
func (s bizWindowState) headroom(scope, limitType string, limit int) bizHeadroom {
	if !s.Valid {
		return bizHeadroom{}
	}
	remaining := int64(limit) - s.Used
	if remaining < 0 {
		remaining = 0
	}
	return bizHeadroom{
		Valid:     true,
		Scope:     scope,
		LimitType: limitType,
		Limit:     limit,
		Remaining: remaining,
		ResetUnix: ceilMsToUnixSec(s.OldestMs + bizRateLimitWindow.Milliseconds()),
	}
}

// ceilMsToUnixSec converts a unix-millisecond instant to unix seconds,
// rounding UP. setRateLimitHeadroomHeaders' Reset and bizRetryAfterSec's
// Retry-After describe the SAME instant (the oldest in-window admission
// aging out) in two different units; truncating (plain /1000) would let
// Reset land up to 999ms earlier than the Retry-After a 429 issued at the
// same moment would have promised.
func ceilMsToUnixSec(ms int64) int64 {
	return (ms + 999) / 1000
}

// bizHeadroom is one admitted check's self-pacing snapshot. BusinessRateLimit
// and BusinessModelRateLimit each run several checks (token/tenant/model ×
// rpm/tpm) and keep only the tightest — the one a client is closest to
// tripping is the useful one to publish, not whichever ran first.
type bizHeadroom struct {
	Valid     bool
	Scope     string
	LimitType string
	Limit     int
	Remaining int64
	ResetUnix int64
}

// ratio is the fraction of budget left. Only meaningful when Valid and
// Limit > 0, both guaranteed by every caller (0 = unlimited dimensions never
// reach a headroom check).
func (h bizHeadroom) ratio() float64 {
	return float64(h.Remaining) / float64(h.Limit)
}

// tighterThan reports whether h is a stricter (lower-ratio) headroom than
// other. An invalid other always loses (nothing to beat); an invalid h never
// wins (nothing to publish).
func (h bizHeadroom) tighterThan(other bizHeadroom) bool {
	if !h.Valid {
		return false
	}
	if !other.Valid {
		return true
	}
	return h.ratio() < other.ratio()
}

// bizHeadroomFromHeaders reconstructs the headroom an earlier middleware in
// the chain already wrote to the response (BusinessRateLimit runs before
// BusinessModelRateLimit), so the later check can decide whether it is
// tighter without threading extra state through the gin context. Missing or
// unparseable headers mean "nothing on the writer yet".
func bizHeadroomFromHeaders(c *gin.Context) bizHeadroom {
	h := c.Writer.Header()
	limitStr := h.Get("X-RateLimit-Limit")
	remStr := h.Get("X-RateLimit-Remaining")
	if limitStr == "" || remStr == "" {
		return bizHeadroom{}
	}
	limit, errL := strconv.Atoi(limitStr)
	remaining, errR := strconv.ParseInt(remStr, 10, 64)
	if errL != nil || errR != nil || limit <= 0 {
		return bizHeadroom{}
	}
	return bizHeadroom{Valid: true, Limit: limit, Remaining: remaining}
}

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

// allow admits or denies one request for key under limit within window. On
// denial it returns the Retry-After seconds derived from the oldest
// in-window admission. Memory stays bounded by the set of active keys:
// out-of-window timestamps are pruned on every touch. The returned
// bizWindowState reports the window occupancy AFTER this call — used=len(ts)
// on deny (the request was not added), used=len(ts)+1 on admit; oldestMs is
// ts[0] of that same post-call slice, which on the first-ever admission into
// an empty window is the new stamp itself (so its headroom reset lands at
// now+window, not some stale zero).
func (l *bizMemoryLimiter) allow(key string, limit int, window time.Duration, now time.Time) (bool, int64, bizWindowState) {
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
		return false, bizRetryAfterSec(ts[0], nowMs, window), bizWindowState{Valid: true, Used: int64(len(ts)), OldestMs: ts[0]}
	}
	ts = append(ts, nowMs)
	l.entries[key] = ts
	return true, 0, bizWindowState{Valid: true, Used: int64(len(ts)), OldestMs: ts[0]}
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
// The admit path folds a ZRANGE 0 0 WITHSCORES into the ZADD+EXPIRE pipeline
// so the headroom snapshot costs no extra round trip; if that pipeline
// errors the request still admits (fail open) but bizWindowState comes back
// invalid — a value this call never actually observed must not be published.
func bizRedisAllow(ctx context.Context, rdb *redis.Client, key string, limit int, window time.Duration, now time.Time) (bool, int64, bizWindowState, error) {
	nowMs := now.UnixMilli()
	cutoff := strconv.FormatInt(nowMs-window.Milliseconds(), 10)

	if err := rdb.ZRemRangeByScore(ctx, key, "-inf", cutoff).Err(); err != nil {
		return false, 0, bizWindowState{}, fmt.Errorf("biz rate limit zremrangebyscore: %w", err)
	}
	count, err := rdb.ZCard(ctx, key).Result()
	if err != nil {
		return false, 0, bizWindowState{}, fmt.Errorf("biz rate limit zcard: %w", err)
	}
	if count >= int64(limit) {
		oldest, err := rdb.ZRangeWithScores(ctx, key, 0, 0).Result()
		if err != nil {
			return false, 0, bizWindowState{}, fmt.Errorf("biz rate limit zrange: %w", err)
		}
		if len(oldest) == 0 {
			// Raced empty between ZCard and ZRange; be conservative.
			return false, 1, bizWindowState{}, nil
		}
		oldestMs := int64(oldest[0].Score)
		return false, bizRetryAfterSec(oldestMs, nowMs, window), bizWindowState{Valid: true, Used: count, OldestMs: oldestMs}, nil
	}

	member := strconv.FormatInt(now.UnixNano(), 10) + "-" + strconv.FormatUint(bizMemberSeq.Add(1), 10)
	pipe := rdb.Pipeline()
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(nowMs), Member: member})
	pipe.Expire(ctx, key, window+10*time.Second)
	rangeCmd := pipe.ZRangeWithScores(ctx, key, 0, 0)
	if _, err := pipe.Exec(ctx); err != nil {
		// Admission already decided; a failed record only under-counts.
		return true, 0, bizWindowState{}, fmt.Errorf("biz rate limit zadd: %w", err)
	}
	oldest, err := rangeCmd.Result()
	if err != nil || len(oldest) == 0 {
		// Pipeline itself succeeded but the range result is unusable —
		// admission stands, headroom does not.
		return true, 0, bizWindowState{}, nil //nolint:nilerr // admission already decided; an unusable range result only drops headroom, it is not a limiter error
	}
	return true, 0, bizWindowState{Valid: true, Used: count + 1, OldestMs: int64(oldest[0].Score)}, nil
}

// bizAllow routes to the Redis or in-memory window. Errors fail open (allow)
// with an ops-visible log line and an invalid bizWindowState (no headroom
// header for that check).
func bizAllow(c *gin.Context, key string, limit int) (bool, int64, bizWindowState) {
	now := bizNow()
	if common.RedisEnabled && common.RDB != nil {
		allowed, retryAfter, state, err := bizRedisAllow(c.Request.Context(), common.RDB, key, limit, bizRateLimitWindow, now)
		if err != nil {
			common.SysError("business rate limit backend error, failing open: " + err.Error())
			return true, 0, bizWindowState{}
		}
		return allowed, retryAfter, state
	}
	return bizMemLimiter.allow(key, limit, bizRateLimitWindow, now)
}

// bizReject emits the 429: Retry-After + X-RateLimit-* headers (Scope/Type
// name the check that tripped — token|tenant rpm|tpm from BusinessRateLimit
// right here, model rpm|tpm from BusinessModelRateLimit
// (business_model_rate_limit.go), token|tenant concurrency from
// RelayConcurrencyLimit (concurrency_limit.go), user requests from
// ModelRequestRateLimit (model-rate-limit.go), key|ip requests from the
// keyed reject sites in rate-limit.go; the same two headers are also written
// by the entitlement gate (account/quota, entitlement.go) and the cost-spike
// fuse (user/cost, cost_spike.go), which do not go through this function),
// the relay error-format JSON body,
// and the rate-limited counter (scope × limitType, limitType ∈
// {"rpm","tpm"} for this function).
//
// An earlier check in the same request (e.g. BusinessRateLimit's token/tenant
// pass) may already have written an ADMIT-path headroom snapshot to this
// response — including X-RateLimit-Reset, which this function has no
// replacement value for (a reject carries Retry-After, not a window Reset).
// Clear everything first so no field from that unrelated, tighter-seeming
// check survives next to this reject's own values.
func bizReject(c *gin.Context, scope, limitType string, limit int, retryAfter int64) {
	metrics.RecordRateLimited(scope, limitType)
	ClearRateLimitHeadroomHeaders(c)
	setRateLimitResponseHeaders(c, limit, 0, retryAfter)
	c.Writer.Header().Set("X-RateLimit-Scope", scope)
	c.Writer.Header().Set("X-RateLimit-Type", limitType)
	measure := "requests"
	if limitType == "tpm" {
		measure = "tokens"
	}
	abortWithOpenAiMessage(c, http.StatusTooManyRequests,
		fmt.Sprintf("%s %s limit exceeded: %d per minute (%s); retry after %d s", scope, measure, limit, bizRateLimitErrorCode, retryAfter),
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
// Returns false when the request was rejected (429 already written); on
// admit the second value is the headroom snapshot for this check (remaining
// = limit - total - estimate, always >= 0 since admission required
// total+estimate <= limit; reset = the oldest settled record's window exit,
// or now+window when the window is empty).
func bizTPMAdmit(c *gin.Context, scope string, limit int, total, oldestMs int64, err error) (bool, bizHeadroom) {
	if err != nil {
		common.SysError("business tpm limit backend error, failing open: " + err.Error())
		return true, bizHeadroom{}
	}
	estimate := int64(common.GetContextKeyInt(c, constant.ContextKeyPromptTokens))
	if estimate < 0 {
		estimate = 0
	}
	if total+estimate <= int64(limit) {
		// Empty window (no settled usage yet this minute): there is no real
		// oldest-record deadline to ceil, so this is a synthetic "next window
		// starts one full period from now" estimate, not a promise the way
		// the oldestMs branch below is (that one mirrors bizRetryAfterSec's
		// ceil exactly, because a real 429 issued at the same instant would
		// promise the same deadline). .Unix() truncates the current
		// sub-second remainder — a <1s float relative to a 60s window —
		// deliberately left as a floor: rounding it up would claim a
		// precision about "when TPM usage was last recorded" that does not
		// exist yet in an empty window.
		resetUnix := app.BizTPMNow().Unix() + int64(app.BizTPMWindow/time.Second)
		if oldestMs > 0 {
			resetUnix = ceilMsToUnixSec(oldestMs + app.BizTPMWindow.Milliseconds())
		}
		hr := bizHeadroom{
			Valid:     true,
			Scope:     scope,
			LimitType: "tpm",
			Limit:     limit,
			Remaining: int64(limit) - total - estimate,
			ResetUnix: resetUnix,
		}
		return true, hr
	}
	// Retry-After: when the oldest in-window record slides out. With an empty
	// window (deny driven purely by an over-limit estimate) there is nothing
	// to wait for — clamp to the 1s minimum like bizRetryAfterSec does.
	retryAfter := int64(1)
	if oldestMs > 0 {
		retryAfter = bizRetryAfterSec(oldestMs, app.BizTPMNow().UnixMilli(), bizRateLimitWindow)
	}
	bizReject(c, scope, "tpm", limit, retryAfter)
	return false, bizHeadroom{}
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

		// best tracks the tightest headroom across every check this request
		// actually ran; written once, right before c.Next(), so it precedes
		// the first byte a streaming handler flushes.
		var best bizHeadroom

		if tokenLimits.RPM > 0 {
			allowed, retryAfter, state := bizAllow(c, bizTokenKeyPrefix+strconv.Itoa(tokenID), tokenLimits.RPM)
			if !allowed {
				bizReject(c, "token", "rpm", tokenLimits.RPM, retryAfter)
				return
			}
			if hr := state.headroom("token", "rpm", tokenLimits.RPM); hr.tighterThan(best) {
				best = hr
			}
		}
		if tokenLimits.TPM > 0 {
			total, oldestMs, qerr := app.QueryBusinessTPMTokenWindow(c.Request.Context(), tokenID)
			ok, hr := bizTPMAdmit(c, "token", tokenLimits.TPM, total, oldestMs, qerr)
			if !ok {
				return
			}
			if hr.tighterThan(best) {
				best = hr
			}
		}

		if tenantID != "" {
			tenantLimits, err := fetchTenantBizLimits(tenantID)
			if err != nil {
				common.SysError(fmt.Sprintf("business rate limit: tenant %s limit lookup failed, failing open: %s", tenantID, err.Error()))
			} else {
				if tenantLimits.RPM > 0 {
					allowed, retryAfter, state := bizAllow(c, bizTenantKeyPrefix+tenantID, tenantLimits.RPM)
					if !allowed {
						bizReject(c, "tenant", "rpm", tenantLimits.RPM, retryAfter)
						return
					}
					if hr := state.headroom("tenant", "rpm", tenantLimits.RPM); hr.tighterThan(best) {
						best = hr
					}
				}
				if tenantLimits.TPM > 0 {
					total, oldestMs, qerr := app.QueryBusinessTPMTenantWindow(c.Request.Context(), tenantID)
					ok, hr := bizTPMAdmit(c, "tenant", tenantLimits.TPM, total, oldestMs, qerr)
					if !ok {
						return
					}
					if hr.tighterThan(best) {
						best = hr
					}
				}
			}
		}

		if bizHeadroomEnabled && best.Valid {
			setRateLimitHeadroomHeaders(c, best.Scope, best.LimitType, best.Limit, best.Remaining, best.ResetUnix)
		}
		c.Next()
	}
}
