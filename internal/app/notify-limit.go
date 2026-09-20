package app

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// notifyLimitStore is used for in-memory rate limiting when Redis is disabled
var (
	notifyLimitStore sync.Map
	cleanupOnce      sync.Once
	cleanupCtx       context.Context
	cleanupCancel    context.CancelFunc
)

type limitCount struct {
	Count     int
	Timestamp time.Time
}

// defaultNotifyLimitDurationMinute 与 NOTIFICATION_LIMIT_DURATION_MINUTE 的
// 默认值一致，用于兜住非法配置。
const defaultNotifyLimitDurationMinute = 10

func getDuration() time.Duration {
	minute := constant.NotificationLimitDurationMinute
	if minute <= 0 {
		// 窗口配置为 0/负数时不能直接使用：Redis 计数键会被写成永不过期，
		// 内存计数也会被判定为立即过期，两条路径的限流都会静默失效。
		minute = defaultNotifyLimitDurationMinute
	}
	return time.Duration(minute) * time.Minute
}

// InitNotifyLimitCleanup initializes the cleanup task with context support.
// Call this from main.go with a context that will be cancelled on shutdown.
func InitNotifyLimitCleanup(ctx context.Context) {
	cleanupOnce.Do(func() {
		cleanupCtx, cleanupCancel = context.WithCancel(ctx)
		go startCleanupTaskWithContext(cleanupCtx)
	})
}

// StopNotifyLimitCleanup stops the cleanup task gracefully.
func StopNotifyLimitCleanup() {
	if cleanupCancel != nil {
		cleanupCancel()
	}
}

// resetNotifyLimitCleanupForTest clears the cleanup singleton so a test can
// exercise InitNotifyLimitCleanup and checkMemoryLimit's lazy-start path
// against a fresh sync.Once, regardless of what earlier tests in this binary
// already did to the shared package state. Matches this codebase's *ForTest
// reset convention (e.g. totp.ResetStateForTest, resetRankingsCacheForTest).
// It is meant for tests: in production cleanupOnce fires once per process
// and there is no reason to rearm it.
func resetNotifyLimitCleanupForTest() {
	cleanupOnce = sync.Once{}
	cleanupCtx = nil
	cleanupCancel = nil
}

// startCleanupTaskWithContext starts a background task to clean up expired entries.
// It respects context cancellation for graceful shutdown.
func startCleanupTaskWithContext(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			common.SysLog("notify limit cleanup task stopped")
			return
		case <-ticker.C:
			now := time.Now()
			notifyLimitStore.Range(func(key, value interface{}) bool {
				if limit, ok := value.(limitCount); ok {
					if now.Sub(limit.Timestamp) >= getDuration() {
						notifyLimitStore.Delete(key)
					}
				}
				return true
			})
		}
	}
}

// CheckNotificationLimit checks if the user has exceeded their notification limit
// Returns true if the user can send notification, false if limit exceeded
func CheckNotificationLimit(ctx context.Context, userId int, notifyType string) (bool, error) {
	if common.RedisEnabled {
		return checkRedisLimit(ctx, userId, notifyType)
	}
	return checkMemoryLimit(userId, notifyType)
}

func checkRedisLimit(ctx context.Context, userId int, notifyType string) (bool, error) {
	key := fmt.Sprintf("notify_limit:%d:%s:%s", userId, notifyType, time.Now().Format("2006010215"))

	// Get current count
	count, err := common.RedisGet(ctx, key)
	if err != nil && err.Error() != "redis: nil" {
		return false, fmt.Errorf("failed to get notification count: %w", err)
	}

	// If key doesn't exist, initialize it
	if count == "" {
		err = common.RedisSet(ctx, key, "1", getDuration())
		return true, err
	}

	currentCount, _ := strconv.Atoi(count)
	limit := constant.NotifyLimitCount

	// Check if limit is already reached
	if currentCount >= limit {
		return false, nil
	}

	// Only increment if under limit
	err = common.RedisIncr(ctx, key, 1)
	if err != nil {
		return false, fmt.Errorf("failed to increment notification count: %w", err)
	}

	return true, nil
}

func checkMemoryLimit(userId int, notifyType string) (bool, error) {
	// Ensure cleanup task is started. Routed through the same Once-guarded
	// InitNotifyLimitCleanup main.go calls at boot (cycle 13 L11 fix) —
	// this used to call the deprecated ctx-less startCleanupTask directly
	// through the same cleanupOnce, so whichever of the two callers ran
	// first silently won and the loser's semantics were dropped: with this
	// path winning the race, cleanupCancel stayed nil and
	// StopNotifyLimitCleanup had nothing to cancel, so the goroutine
	// outlived the shutdown it was supposed to observe.
	// context.Background() here matters if this genuinely wins the race
	// against main.go's boot-time call, which takes a request arriving
	// before the HTTP server starts accepting traffic; either way the
	// goroutine this starts is reachable through StopNotifyLimitCleanup,
	// which the previous shape could not manage.
	InitNotifyLimitCleanup(context.Background())

	key := fmt.Sprintf("%d:%s:%s", userId, notifyType, time.Now().Format("2006010215"))
	now := time.Now()

	// Get current limit count or initialize new one
	var currentLimit limitCount
	if value, ok := notifyLimitStore.Load(key); ok {
		currentLimit = value.(limitCount)
		// Check if the entry has expired
		if now.Sub(currentLimit.Timestamp) >= getDuration() {
			currentLimit = limitCount{Count: 0, Timestamp: now}
		}
	} else {
		currentLimit = limitCount{Count: 0, Timestamp: now}
	}

	// Increment count
	currentLimit.Count++

	// Check against limits
	limit := constant.NotifyLimitCount

	// Store updated count
	notifyLimitStore.Store(key, currentLimit)

	return currentLimit.Count <= limit, nil
}
