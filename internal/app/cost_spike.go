package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// Cost-spike protection — per-user 5-minute sliding window via Redis sorted set.
// Ported from 2b-svc-newapi/service/quota.go + middleware/cost_spike.go (2026-05-07).
//
// The middleware (internal/adapter/middleware/cost_spike.go) reads this window
// before each /v1/* relay call; PostConsumeQuota writes to it after every
// successful settlement. Both fail open when Redis is unreachable.

const (
	CostSpikeKeyPrefix  = "cost_spike:user:"
	CostSpikeWindowSecs = 5 * 60
	CostSpikeTTLSecs    = 600
)

// QueryCostSpikeWindow returns the total tokens consumed by userID in the
// last 5 minutes. Evicts expired entries before summing.
// ctx carries the timeout; callers should provide one (suggested ≤ 1s).
func QueryCostSpikeWindow(ctx context.Context, rdb *redis.Client, userID int) (int64, error) {
	key := CostSpikeKeyPrefix + strconv.Itoa(userID)
	cutoff := time.Now().Add(-CostSpikeWindowSecs * time.Second).UnixMilli()

	if err := rdb.ZRemRangeByScore(ctx, key, "-inf", strconv.FormatInt(cutoff, 10)).Err(); err != nil {
		return 0, fmt.Errorf("cost_spike zremrangebyscore: %w", err)
	}

	members, err := rdb.ZRange(ctx, key, 0, -1).Result()
	if err != nil {
		return 0, fmt.Errorf("cost_spike zrange: %w", err)
	}

	var total int64
	for _, m := range members {
		total += parseCostSpikeMember(m)
	}
	return total, nil
}

// parseCostSpikeMember extracts the token count from a "<ts_ms>:<tokens>"
// member. Returns 0 on malformed entries — silently skipping is safer than
// returning an error since one bad entry shouldn't fail the whole window read.
func parseCostSpikeMember(m string) int64 {
	parts := strings.SplitN(m, ":", 2)
	if len(parts) != 2 {
		return 0
	}
	tokens, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0
	}
	return tokens
}

// RecordCostSpikeWindow appends token usage to the per-user 5-minute sliding
// window. No-op when Redis is unavailable, protection is disabled, or tokens
// is non-positive (refunds shouldn't count). Designed to run async in a
// goroutine — errors are logged, not propagated.
func RecordCostSpikeWindow(userID, tokens int) {
	if !common.CostSpikeProtectionEnabled || !common.RedisEnabled || tokens <= 0 {
		return
	}
	if common.RDB == nil {
		return
	}
	// 3s, not 1s: measured live 2026-08-29 — the first write after an idle
	// period blocked for the full 1s budget and died on context-deadline
	// ("cost_spike record failed ... context deadline exceeded") while the
	// very next command on a sibling goroutine succeeded instantly. A cold
	// pooled connection pays dial + DNS that 1s does not absorb; on a
	// low-traffic gateway that makes the FIRST settlement after every idle
	// gap the one most likely to vanish from the window. Same bump as
	// bizTPMRedisTimeout (business_tpm.go), which failed identically in the
	// same live session.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	now := time.Now().UnixMilli()
	key := fmt.Sprintf("%s%d", CostSpikeKeyPrefix, userID)
	member := fmt.Sprintf("%d:%d", now, tokens)
	pipe := common.RDB.Pipeline()
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(now), Member: member})
	pipe.Expire(ctx, key, CostSpikeTTLSecs*time.Second)
	if _, err := pipe.Exec(ctx); err != nil {
		common.SysLog(fmt.Sprintf("cost_spike record failed for user %d: %s", userID, err.Error()))
	}
}
