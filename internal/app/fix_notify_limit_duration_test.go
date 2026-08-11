package app

// fix_notify_limit_duration_test.go — 回归测试：NOTIFICATION_LIMIT_DURATION_MINUTE
// 被配成 0（或负数）时，getDuration() 返回 0，导致
//   - Redis 路径：计数键被写成永不过期，后续自增全部落空，限流永久失效；
//   - 内存路径：任何一条记录都会被判定为“已过期”而清零，同样永不触发限流。
// 修复后 getDuration() 回退到默认窗口，两条路径都恢复限流。

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// fixNotifyLimitMiniRedis 把 common.RDB 指向进程内 miniredis，结束后恢复。
func fixNotifyLimitMiniRedis(t *testing.T) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	prevRDB, prevEnabled := common.RDB, common.RedisEnabled
	common.RDB, common.RedisEnabled = client, true
	t.Cleanup(func() {
		common.RDB, common.RedisEnabled = prevRDB, prevEnabled
		_ = client.Close()
		mr.Close()
	})
}

// fixNotifyLimitZeroWindow 把窗口配置设成 0（非法值），结束后恢复。
func fixNotifyLimitZeroWindow(t *testing.T, limit int) {
	t.Helper()
	prevDur, prevLimit := constant.NotificationLimitDurationMinute, constant.NotifyLimitCount
	constant.NotificationLimitDurationMinute = 0
	constant.NotifyLimitCount = limit
	t.Cleanup(func() {
		constant.NotificationLimitDurationMinute = prevDur
		constant.NotifyLimitCount = prevLimit
	})
}

// TestFixGetDuration_NonPositiveConfigFallsBack: 0/负数配置必须回退到正数窗口。
func TestFixGetDuration_NonPositiveConfigFallsBack(t *testing.T) {
	prev := constant.NotificationLimitDurationMinute
	t.Cleanup(func() { constant.NotificationLimitDurationMinute = prev })

	for _, minute := range []int{0, -5} {
		constant.NotificationLimitDurationMinute = minute
		if d := getDuration(); d <= 0 {
			t.Errorf("getDuration() with %d minute = %v, want a positive window", minute, d)
		}
	}
}

// TestFixNotifyLimit_ZeroWindowStillLimitsRedis: 窗口配 0 时 Redis 路径仍然限流，
// 且计数键带上了过期时间。修复前计数键永不过期 → RedisIncr 空操作 → 计数停在 "1"
// → 每次调用都被放行。
func TestFixNotifyLimit_ZeroWindowStillLimitsRedis(t *testing.T) {
	fixNotifyLimitMiniRedis(t)
	fixNotifyLimitZeroWindow(t, 2)

	ctx := context.Background()
	userID := 880001
	notifyType := "fix-zero-window"
	key := fmt.Sprintf("notify_limit:%d:%s:%s", userID, notifyType, time.Now().Format("2006010215"))

	// 前两次在限额内
	for i := 0; i < 2; i++ {
		allowed, err := checkRedisLimit(ctx, userID, notifyType)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if !allowed {
			t.Fatalf("call %d should be admitted (limit=2)", i)
		}
	}
	// 计数键必须有过期时间，否则窗口不会滚动
	if ttl := common.RDB.TTL(ctx, key).Val(); ttl <= 0 {
		t.Errorf("counter key TTL = %v, want a positive expiry", ttl)
	}
	// 计数必须真的涨到限额
	if v, err := common.RDB.Get(ctx, key).Int(); err != nil || v != 2 {
		t.Errorf("counter = %d (err=%v), want 2", v, err)
	}
	// 第三次必须被拦下
	allowed, err := checkRedisLimit(ctx, userID, notifyType)
	if err != nil {
		t.Fatalf("third call: %v", err)
	}
	if allowed {
		t.Error("third notification must be rejected once the limit is reached")
	}
}

// TestFixNotifyLimit_ZeroWindowStillLimitsMemory: 无 Redis 时同样要限流。
// 修复前 now.Sub(ts) >= 0 恒成立，计数每次都被重置为 0，永远放行。
func TestFixNotifyLimit_ZeroWindowStillLimitsMemory(t *testing.T) {
	fixNotifyLimitZeroWindow(t, 2)

	userID := 880002
	notifyType := "fix-zero-window-mem"
	for i := 0; i < 2; i++ {
		allowed, err := checkMemoryLimit(userID, notifyType)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if !allowed {
			t.Fatalf("call %d should be admitted (limit=2)", i)
		}
	}
	allowed, err := checkMemoryLimit(userID, notifyType)
	if err != nil {
		t.Fatalf("third call: %v", err)
	}
	if allowed {
		t.Error("third notification must be rejected once the limit is reached")
	}
}
