package middleware

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// In-flight concurrency limiting — a cap on how many relay requests one token
// (or one tenant) may have OPEN at the same time.
//
// WHY THIS IS NOT COVERED BY RPM/TPM: those meter arrivals and settled usage
// over a minute. A streaming request can sit open for ten minutes while
// counting as a single request — so a client with an RPM of 60 can still hold
// hundreds of simultaneous upstream connections, exhaust the shared relay
// connection pool (RELAY_MAX_IDLE_CONNS) and starve every other tenant. Rate
// limits bound the arrival rate; only a concurrency limit bounds occupancy.
//
// LEASE, NOT COUNTER: the slot set is a Redis ZSET keyed by a per-request lease
// id with the admission timestamp as score. Admission first drops members older
// than the lease TTL, so a replica that crashes mid-request cannot leak a slot
// forever — the worst case is one stale slot for the TTL, versus a plain
// INCR/DECR counter that would drift upward permanently. Release removes the
// member; a missed release self-heals at the TTL.
//
// Defaults to OFF (limit 0). Every backend error fails OPEN, matching
// BusinessRateLimit / CostSpikeLimit: infrastructure trouble must not become a
// service outage.
//
// Position in chain: AFTER TokenAuth (needs "token_id"), and the slot must be
// held across the whole relay, so this middleware wraps c.Next().

const (
	concurrencyTokenKeyPrefix  = "cc:tok:"
	concurrencyTenantKeyPrefix = "cc:tenant:"
	// concurrencyLimitErrorCode is the machine-readable code in the 429 body,
	// distinct from the RPM/TPM limiter so clients can tell "too many at once"
	// from "too many per minute" — the correct client reaction differs
	// (drain in-flight work vs. back off on the clock).
	concurrencyLimitErrorCode = "concurrency_limit_exceeded"
	// concurrencyDefaultLeaseTTL bounds how long a leaked slot can survive.
	// Deliberately longer than any legitimate relay: reclaiming a slot from a
	// still-streaming request would let the limit be exceeded.
	concurrencyDefaultLeaseTTLSeconds = 1800
)

// RelayMaxConcurrentPerToken / PerTenant read their env on every call so an
// operator can flip the cap without a restart in the same way SYNC_FREQUENCY
// style knobs behave. 0 (default) disables that dimension entirely.
func concurrencyLimitPerToken() int {
	return common.GetEnvOrDefault("RELAY_MAX_CONCURRENT_PER_TOKEN", 0)
}

func concurrencyLimitPerTenant() int {
	return common.GetEnvOrDefault("RELAY_MAX_CONCURRENT_PER_TENANT", 0)
}

func concurrencyLeaseTTL() time.Duration {
	secs := common.GetEnvOrDefault("RELAY_CONCURRENCY_LEASE_TTL", concurrencyDefaultLeaseTTLSeconds)
	if secs <= 0 {
		secs = concurrencyDefaultLeaseTTLSeconds
	}
	return time.Duration(secs) * time.Second
}

// ─── Redis backend ───────────────────────────────────────────────────────────

// ccRedisAcquire drops expired leases, then admits iff the live count is under
// limit. Returns (admitted, error); a non-nil error means "backend unavailable"
// and callers fail open.
func ccRedisAcquire(ctx context.Context, rdb *redis.Client, key, leaseID string, limit int, ttl time.Duration, now time.Time) (bool, error) {
	cutoff := fmt.Sprintf("%d", now.Add(-ttl).UnixMilli())
	if err := rdb.ZRemRangeByScore(ctx, key, "-inf", cutoff).Err(); err != nil {
		return false, err
	}
	count, err := rdb.ZCard(ctx, key).Result()
	if err != nil {
		return false, err
	}
	if count >= int64(limit) {
		return false, nil
	}
	pipe := rdb.TxPipeline()
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(now.UnixMilli()), Member: leaseID})
	// Key-level expiry is a second safety net: if every holder dies at once,
	// the whole set disappears rather than pinning the limit forever.
	pipe.Expire(ctx, key, ttl*2)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func ccRedisRelease(ctx context.Context, rdb *redis.Client, key, leaseID string) {
	// Best effort: a failed release is reclaimed by the TTL sweep above.
	_ = rdb.ZRem(ctx, key, leaseID).Err()
}

// ─── In-process backend (no Redis) ───────────────────────────────────────────

// Single-node semantics, same as the RPM limiter's Redis-less tier.
var (
	ccLocalMu    sync.Mutex
	ccLocalSlots = map[string]map[string]time.Time{} // key → leaseID → admitted
)

func ccLocalAcquire(key, leaseID string, limit int, ttl time.Duration, now time.Time) bool {
	ccLocalMu.Lock()
	defer ccLocalMu.Unlock()

	slots := ccLocalSlots[key]
	if slots == nil {
		slots = map[string]time.Time{}
		ccLocalSlots[key] = slots
	}
	for id, at := range slots {
		if now.Sub(at) > ttl {
			delete(slots, id)
		}
	}
	if len(slots) >= limit {
		return false
	}
	slots[leaseID] = now
	return true
}

func ccLocalRelease(key, leaseID string) {
	ccLocalMu.Lock()
	defer ccLocalMu.Unlock()
	if slots := ccLocalSlots[key]; slots != nil {
		delete(slots, leaseID)
		if len(slots) == 0 {
			delete(ccLocalSlots, key)
		}
	}
}

// resetConcurrencyLocalForTest clears the in-process slot table between tests.
func resetConcurrencyLocalForTest() {
	ccLocalMu.Lock()
	defer ccLocalMu.Unlock()
	ccLocalSlots = map[string]map[string]time.Time{}
}

// ─── Middleware ──────────────────────────────────────────────────────────────

// ccAcquire routes to whichever backend is configured. Returns
// (admitted, release, ok) where ok=false means the backend errored and the
// caller should fail open.
func ccAcquire(ctx context.Context, key, leaseID string, limit int, ttl time.Duration) (admitted bool, release func(), ok bool) {
	now := time.Now()
	if common.RedisEnabled && common.RDB != nil {
		got, err := ccRedisAcquire(ctx, common.RDB, key, leaseID, limit, ttl, now)
		if err != nil {
			common.SysError("concurrency limit backend error, failing open: " + err.Error())
			return false, nil, false
		}
		if !got {
			return false, nil, true
		}
		// Release deliberately does NOT inherit ctx: on a client disconnect the
		// request context is already cancelled when this deferred call runs, so
		// reusing it would fail the ZREM and leak the slot until its TTL. A
		// fresh bounded context is the only way the release can actually land.
		//nolint:contextcheck // intentional detachment, see above
		return true, func() {
			relCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			ccRedisRelease(relCtx, common.RDB, key, leaseID)
		}, true
	}

	if !ccLocalAcquire(key, leaseID, limit, ttl, now) {
		return false, nil, true
	}
	return true, func() { ccLocalRelease(key, leaseID) }, true
}

func ccReject(c *gin.Context, scope string, limit int) {
	metrics.RecordRateLimited(scope, "concurrency")
	scopeLabel := "令牌"
	if scope == "tenant" {
		scopeLabel = "租户"
	}
	// Retry-After 1s: unlike a per-minute window there is no deterministic
	// reset instant — a slot frees when some in-flight request finishes.
	setRateLimitResponseHeaders(c, limit, 0, 1)
	abortWithOpenAiMessage(c, http.StatusTooManyRequests,
		fmt.Sprintf("%s并发请求数已达上限（%d），请等待进行中的请求完成后重试", scopeLabel, limit),
		concurrencyLimitErrorCode)
}

// RelayConcurrencyLimit caps simultaneous in-flight relay requests per token
// and per tenant. Both dimensions default to 0 (disabled).
func RelayConcurrencyLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenLimit := concurrencyLimitPerToken()
		tenantLimit := concurrencyLimitPerTenant()
		if tokenLimit <= 0 && tenantLimit <= 0 {
			c.Next()
			return
		}

		ttl := concurrencyLeaseTTL()
		leaseID := common.GetUUID()
		var releases []func()
		// LIFO release so the tenant slot (acquired second) is freed first —
		// keeps the two sets consistent if a release panics.
		defer func() {
			for i := len(releases) - 1; i >= 0; i-- {
				releases[i]()
			}
		}()

		if tokenLimit > 0 {
			if tokenID := common.GetContextKeyInt(c, constant.ContextKeyTokenId); tokenID > 0 {
				key := fmt.Sprintf("%s%d", concurrencyTokenKeyPrefix, tokenID)
				admitted, release, ok := ccAcquire(c.Request.Context(), key, leaseID, tokenLimit, ttl)
				if ok && !admitted {
					ccReject(c, "token", tokenLimit)
					return
				}
				if release != nil {
					releases = append(releases, release)
				}
			}
		}

		if tenantLimit > 0 {
			if tenantID := c.GetString("tenant_id"); tenantID != "" {
				key := concurrencyTenantKeyPrefix + tenantID
				admitted, release, ok := ccAcquire(c.Request.Context(), key, leaseID, tenantLimit, ttl)
				if ok && !admitted {
					ccReject(c, "tenant", tenantLimit)
					return
				}
				if release != nil {
					releases = append(releases, release)
				}
			}
		}

		c.Next()
	}
}
