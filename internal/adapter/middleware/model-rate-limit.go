package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common/limiter"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	ModelRequestRateLimitCountMark        = "MRRL"
	ModelRequestRateLimitSuccessCountMark = "MRRLS"
)

// r6aRateLimitDegradedLogLast throttles the fail-open log line below to at
// most once per minute per check ("success"/"total"), mirroring
// warnZeroWalletAmount's per-key throttle (internal/app/quota.go:1032/1064).
// Without this, a sustained Redis outage would emit one ERROR line per relay
// request for as long as the outage lasted, at whatever the live QPS is —
// the metric below (r6aRateLimitDegradedLogf's caller) stays UNCONDITIONAL
// so the counter itself never loses a single occurrence; only the log line
// is rate-limited.
var r6aRateLimitDegradedLogLast sync.Map // checkName(string) -> time.Time

// r6aRateLimitDegradedLogFunc is a seam over common.SysError so tests can
// observe the throttle's actual output (count calls) instead of only
// inferring it from process-log side effects — mirrors the
// zeroWalletWarnLogf seam in internal/app/quota.go, added for the identical
// reason: without it, nothing distinguishes "the throttle suppressed this
// call" from "the throttle never fired at all".
var r6aRateLimitDegradedLogFunc = common.SysError

// r6aTokenBucketAllowFunc seams the total-count token-bucket check
// (limiter.New(ctx, rdb).Allow) behind a package var so tests can force a
// deterministic Redis-backend error on this branch. This is necessary, not
// cosmetic: limiter.New (internal/pkg/common/limiter/limiter.go:26) caches
// its *RedisLimiter behind a process-wide sync.Once and ignores the rdb
// argument on every call after the first, so once any earlier test in this
// package has driven this branch against a live/working Redis (miniredis or
// the real one), a later test's dead-Redis fixture can no longer make this
// call fail — the singleton keeps talking to the first client it ever saw.
// Overriding this var sidesteps that entirely instead of depending on test
// file execution order to keep the singleton unset.
var r6aTokenBucketAllowFunc = func(ctx context.Context, rdb *redis.Client, key string, opts ...limiter.Option) (bool, error) {
	return limiter.New(ctx, rdb).Allow(ctx, key, opts...)
}

// r6aRateLimitDegradedLogf is the throttled log emitter for the fail-open
// branches in redisRateLimitHandler. checkName is "success" or "total".
func r6aRateLimitDegradedLogf(checkName, msg string) {
	now := time.Now()
	if last, loaded := r6aRateLimitDegradedLogLast.LoadOrStore(checkName, now); loaded {
		if now.Sub(last.(time.Time)) < time.Minute {
			return
		}
		r6aRateLimitDegradedLogLast.Store(checkName, now)
	}
	r6aRateLimitDegradedLogFunc(msg)
}

// 检查Redis中的请求限制
func checkRedisRateLimit(ctx context.Context, rdb *redis.Client, key string, maxCount int, duration int64) (bool, error) {
	// 如果maxCount为0，表示不限制
	if maxCount == 0 {
		return true, nil
	}

	// 获取当前计数
	length, err := rdb.LLen(ctx, key).Result()
	if err != nil {
		return false, err
	}

	// 如果未达到限制，允许请求
	if length < int64(maxCount) {
		return true, nil
	}

	// 检查时间窗口
	oldTimeStr, _ := rdb.LIndex(ctx, key, -1).Result()
	oldTime, err := time.Parse(timeFormat, oldTimeStr)
	if err != nil {
		return false, err
	}

	nowTimeStr := time.Now().Format(timeFormat)
	nowTime, err := time.Parse(timeFormat, nowTimeStr)
	if err != nil {
		return false, err
	}
	// 如果在时间窗口内已达到限制，拒绝请求
	subTime := nowTime.Sub(oldTime).Seconds()
	if int64(subTime) < duration {
		rdb.Expire(ctx, key, time.Duration(setting.ModelRequestRateLimitDurationMinutes)*time.Minute)
		return false, nil
	}

	return true, nil
}

// 记录Redis请求
func recordRedisRequest(ctx context.Context, rdb *redis.Client, key string, maxCount int) {
	// 如果maxCount为0，不记录请求
	if maxCount == 0 {
		return
	}

	now := time.Now().Format(timeFormat)
	rdb.LPush(ctx, key, now)
	rdb.LTrim(ctx, key, 0, int64(maxCount-1))
	rdb.Expire(ctx, key, time.Duration(setting.ModelRequestRateLimitDurationMinutes)*time.Minute)
}

// Redis限流处理器
func redisRateLimitHandler(duration int64, totalMaxCount, successMaxCount int) gin.HandlerFunc {
	return func(c *gin.Context) {
		userId := strconv.Itoa(c.GetInt("id"))
		ctx := c.Request.Context()
		rdb := common.RDB

		// 1. 检查成功请求数限制
		successKey := fmt.Sprintf("rateLimit:%s:%s", ModelRequestRateLimitSuccessCountMark, userId)
		allowed, err := checkRedisRateLimit(ctx, rdb, successKey, successMaxCount, duration)
		if err != nil {
			// Fail OPEN, same contract as the sibling limiters
			// (BusinessRateLimit at business_rate_limit.go:311-314,
			// RelayConcurrencyLimit at concurrency_limit.go:35-37): a Redis hiccup
			// must not become a relay outage. This branch was unreachable while
			// setting.ModelRequestRateLimitEnabled defaulted to false; arming that
			// switch (rate_limit.go:52) without this fix would have turned every
			// such error into a 500 for every relay request.
			//
			// Two distinct classes of error land here, and the single metric
			// label below does not separate them — they need different operator
			// responses, so read the throttled log line before acting:
			//   (1) backend unreachable — checkRedisRateLimit's LLen call fails
			//       (model-rate-limit.go:79-82). Fix Redis.
			//   (2) stored data corrupt — the backend is healthy but the value
			//       at the tail of the MRRLS list does not parse as a timestamp
			//       (model-rate-limit.go:92-95). Restarting Redis will not help;
			//       the key needs deleting. Pinned by
			//       TestRedisRateLimitHandler_SuccessCheckError_FailsOpen.
			//
			// NOT silent: metrics.RateLimitDegradedTotal increments on every
			// occurrence (unconditional, unlike the log line below), so the
			// degradation stays visible even if this branch fires faster than
			// the per-minute log throttle can report it.
			//
			// Composite risk while Redis is down (operator decision D1,
			// 2026-08-27): this per-model limiter, BusinessRateLimit /
			// BusinessModelRateLimit (business_rate_limit.go) and
			// RelayConcurrencyLimit (concurrency_limit.go) ALL fail open at the
			// same time, and CostSpikeLimit defaults to observe-only
			// (common.CostSpikeEnforce=false: it counts and logs a breach but
			// does not disable the account or reject the request). During a
			// Redis outage there is therefore no rate or cost ceiling of any
			// kind on relay traffic — a deliberate outage-vs-outage tradeoff,
			// not an oversight.
			metrics.RecordRateLimitDegraded("model_rate_limit_success")
			r6aRateLimitDegradedLogf("success", "rate limit check success count error, failing open: "+err.Error())
			c.Next()
			return
		}
		if !allowed {
			setRateLimitResponseHeaders(c, successMaxCount, 0, duration)
			abortWithOpenAiMessage(c, http.StatusTooManyRequests, fmt.Sprintf("您已达到请求数限制：%d分钟内最多请求%d次", setting.ModelRequestRateLimitDurationMinutes, successMaxCount))
			return
		}

		//2.检查总请求数限制并记录总请求（当totalMaxCount为0时会自动跳过，使用令牌桶限流器
		if totalMaxCount > 0 {
			totalKey := fmt.Sprintf("rateLimit:%s", userId)
			allowed, err = r6aTokenBucketAllowFunc(
				ctx,
				rdb,
				totalKey,
				limiter.WithCapacity(int64(totalMaxCount)*duration),
				limiter.WithRate(int64(totalMaxCount)),
				limiter.WithRequested(duration),
			)

			if err != nil {
				// Same fail-open contract, metric and composite-risk note as
				// the success-count branch above.
				metrics.RecordRateLimitDegraded("model_rate_limit_total")
				r6aRateLimitDegradedLogf("total", "rate limit check total count error, failing open: "+err.Error())
				c.Next()
				return
			}

			if !allowed {
				setRateLimitResponseHeaders(c, totalMaxCount, 0, duration)
				abortWithOpenAiMessage(c, http.StatusTooManyRequests, fmt.Sprintf("您已达到总请求数限制：%d分钟内最多请求%d次，包括失败次数，请检查您的请求是否正确", setting.ModelRequestRateLimitDurationMinutes, totalMaxCount))
			}
		}

		// 4. 处理请求
		c.Next()

		// 5. 如果请求成功，记录成功请求
		if c.Writer.Status() < 400 {
			recordRedisRequest(ctx, rdb, successKey, successMaxCount)
		}
	}
}

// 内存限流处理器
func memoryRateLimitHandler(duration int64, totalMaxCount, successMaxCount int) gin.HandlerFunc {
	inMemoryRateLimiter.Init(time.Duration(setting.ModelRequestRateLimitDurationMinutes) * time.Minute)

	return func(c *gin.Context) {
		userId := strconv.Itoa(c.GetInt("id"))
		totalKey := ModelRequestRateLimitCountMark + userId
		successKey := ModelRequestRateLimitSuccessCountMark + userId

		// 1. 检查总请求数限制（当totalMaxCount为0时跳过）
		if totalMaxCount > 0 && !inMemoryRateLimiter.Request(totalKey, totalMaxCount, duration) {
			setRateLimitResponseHeaders(c, totalMaxCount, 0, duration)
			c.Status(http.StatusTooManyRequests)
			c.Abort()
			return
		}

		// 2. 检查成功请求数限制
		// 使用一个临时key来检查限制，这样可以避免实际记录
		checkKey := successKey + "_check"
		if !inMemoryRateLimiter.Request(checkKey, successMaxCount, duration) {
			setRateLimitResponseHeaders(c, successMaxCount, 0, duration)
			c.Status(http.StatusTooManyRequests)
			c.Abort()
			return
		}

		// 3. 处理请求
		c.Next()

		// 4. 如果请求成功，记录到实际的成功请求计数中
		if c.Writer.Status() < 400 {
			inMemoryRateLimiter.Request(successKey, successMaxCount, duration)
		}
	}
}

// ModelRequestRateLimit 模型请求限流中间件
func ModelRequestRateLimit() func(c *gin.Context) {
	return func(c *gin.Context) {
		// 在每个请求时检查是否启用限流
		if !setting.ModelRequestRateLimitEnabled {
			c.Next()
			return
		}

		// 计算限流参数
		duration := int64(setting.ModelRequestRateLimitDurationMinutes * 60)
		totalMaxCount := setting.ModelRequestRateLimitCount
		successMaxCount := setting.ModelRequestRateLimitSuccessCount

		// 获取分组
		group := common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
		if group == "" {
			group = common.GetContextKeyString(c, constant.ContextKeyUserGroup)
		}

		//获取分组的限流配置
		groupTotalCount, groupSuccessCount, found := setting.GetGroupRateLimit(group)
		if found {
			totalMaxCount = groupTotalCount
			successMaxCount = groupSuccessCount
		}

		// 根据存储类型选择并执行限流处理器
		if common.RedisEnabled {
			redisRateLimitHandler(duration, totalMaxCount, successMaxCount)(c)
		} else {
			memoryRateLimitHandler(duration, totalMaxCount, successMaxCount)(c)
		}
	}
}
