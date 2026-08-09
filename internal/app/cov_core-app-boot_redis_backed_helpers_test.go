package app

// cov_core-app-boot_redis_backed_helpers_test.go — business-acceptance
// coverage for the small real-Redis-backed helpers left at 0%/low coverage
// because every existing test in this package drives quota-threshold/notify
// logic through hand-rolled mocks (mockRedis, fakeDeduper, ...) that never
// touch the real redis.Client wrappers: quota_threshold.go's wrapRedis /
// redisClientDeduper.SetNXBool, and notify-limit.go's checkRedisLimit.
// Uses the withMiniRedisTPM/withoutRedisTPM helpers already defined in
// business_tpm_test.go (same package).

import (
	"context"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/redis/go-redis/v9"
)

// TestCoreAppBootWrapRedis_NilClientReturnsNil locks the nil-safety guard —
// callers (e.g. a boot path where Redis was never configured) must get a nil
// interface, not a non-nil wrapper around a nil *redis.Client that would
// panic on first use.
func TestCoreAppBootWrapRedis_NilClientReturnsNil(t *testing.T) {
	if got := wrapRedis(nil); got != nil {
		t.Fatalf("wrapRedis(nil) = %v, want nil interface", got)
	}
}

// TestCoreAppBootWrapRedis_SetNXBool_FirstThenDuplicate drives the real
// redisClientDeduper.SetNXBool against miniredis: first claim on a key must
// report set=true; a second claim on the same still-live key must report
// set=false (the dedup contract quota_threshold.go's checkAndPublishQuotaThresholds
// depends on to avoid double-firing an alert).
func TestCoreAppBootWrapRedis_SetNXBool_FirstThenDuplicate(t *testing.T) {
	mr := withMiniRedisTPM(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	rdb := wrapRedis(client)
	if rdb == nil {
		t.Fatal("wrapRedis(non-nil client) must not return nil")
	}

	key := "cov:quota_threshold_sent:test-user:50:2026-07"

	first, err := rdb.SetNXBool(context.Background(), key, time.Hour)
	if err != nil {
		t.Fatalf("first SetNXBool: unexpected error: %v", err)
	}
	if !first {
		t.Fatal("expected first SetNXBool on a fresh key to claim it (set=true)")
	}

	second, err := rdb.SetNXBool(context.Background(), key, time.Hour)
	if err != nil {
		t.Fatalf("second SetNXBool: unexpected error: %v", err)
	}
	if second {
		t.Fatal("expected second SetNXBool on the same still-live key to report already-claimed (set=false)")
	}
}

// TestCoreAppBootCheckRedisLimit_InitializesIncrementsAndBlocks drives
// notify-limit.go's checkRedisLimit through its full lifecycle against a
// real (miniredis) Redis: first call initializes the counter and allows,
// subsequent calls under the limit increment and allow, and a call at the
// configured limit is blocked.
func TestCoreAppBootCheckRedisLimit_InitializesIncrementsAndBlocks(t *testing.T) {
	withMiniRedisTPM(t)

	oldLimit, oldDuration := constant.NotifyLimitCount, constant.NotificationLimitDurationMinute
	constant.NotifyLimitCount = 2
	// A positive duration matters here, not just cosmetically: RedisIncr (see
	// internal/pkg/common/redis.go) only increments when the key's TTL > 0 —
	// with duration=0 the initializing RedisSet leaves the key with no TTL,
	// and every subsequent checkRedisLimit call silently fails to increment
	// (see the dedicated FINDING test below). 10 matches the real production
	// default (common.InitEnv's NOTIFICATION_LIMIT_DURATION_MINUTE fallback).
	constant.NotificationLimitDurationMinute = 10
	t.Cleanup(func() {
		constant.NotifyLimitCount = oldLimit
		constant.NotificationLimitDurationMinute = oldDuration
	})

	userId := 990001
	notifyType := "cov-redis-limit"
	ctx := context.Background()

	// Call 1: key doesn't exist yet -> initialize to "1", allowed.
	allowed, err := checkRedisLimit(ctx, userId, notifyType)
	if err != nil {
		t.Fatalf("call 1: unexpected error: %v", err)
	}
	if !allowed {
		t.Fatal("call 1 (initialize) should be allowed")
	}
	got, err := common.RedisGet(ctx, "notify_limit:990001:cov-redis-limit:"+time.Now().Format("2006010215"))
	if err != nil {
		t.Fatalf("expected the counter key to exist after call 1: %v", err)
	}
	if got != "1" {
		t.Fatalf("counter after call 1 = %q, want %q", got, "1")
	}

	// Call 2: count=1 < limit=2 -> increment to 2, allowed.
	allowed, err = checkRedisLimit(ctx, userId, notifyType)
	if err != nil {
		t.Fatalf("call 2: unexpected error: %v", err)
	}
	if !allowed {
		t.Fatal("call 2 should be allowed (still under limit)")
	}

	// Call 3: count=2 >= limit=2 -> blocked, no further increment.
	allowed, err = checkRedisLimit(ctx, userId, notifyType)
	if err != nil {
		t.Fatalf("call 3: unexpected error: %v", err)
	}
	if allowed {
		t.Fatal("call 3 should be blocked once the limit is reached")
	}
	got, err = common.RedisGet(ctx, "notify_limit:990001:cov-redis-limit:"+time.Now().Format("2006010215"))
	if err != nil {
		t.Fatalf("recheck counter: %v", err)
	}
	if got != "2" {
		t.Fatalf("counter after the blocked call = %q, want %q (must not increment past the limit)", got, "2")
	}
}

// TestCoreAppBootCheckRedisLimit_DifferentUsersIndependent verifies the key
// namespacing keeps distinct users from sharing a bucket.
func TestCoreAppBootCheckRedisLimit_DifferentUsersIndependent(t *testing.T) {
	withMiniRedisTPM(t)

	oldLimit := constant.NotifyLimitCount
	constant.NotifyLimitCount = 1
	t.Cleanup(func() { constant.NotifyLimitCount = oldLimit })

	ctx := context.Background()
	notifyType := "cov-redis-independent"

	// Exhaust user A's single-call limit.
	if _, err := checkRedisLimit(ctx, 990002, notifyType); err != nil {
		t.Fatalf("user A call 1: %v", err)
	}
	allowedA, err := checkRedisLimit(ctx, 990002, notifyType)
	if err != nil {
		t.Fatalf("user A call 2: %v", err)
	}
	if allowedA {
		t.Fatal("expected user A to be blocked on its second call at limit=1")
	}

	// User B, same notifyType, must be independent.
	allowedB, err := checkRedisLimit(ctx, 990003, notifyType)
	if err != nil {
		t.Fatalf("user B call 1: %v", err)
	}
	if !allowedB {
		t.Fatal("expected user B's first call to be allowed independently of user A's exhausted limit")
	}
}

// TestCoreAppBootCheckRedisLimit_DispatchedFromCheckNotificationLimit proves
// CheckNotificationLimit's Redis-enabled branch actually reaches
// checkRedisLimit (not just checkMemoryLimit) end to end.
func TestCoreAppBootCheckRedisLimit_DispatchedFromCheckNotificationLimit(t *testing.T) {
	withMiniRedisTPM(t)

	oldLimit := constant.NotifyLimitCount
	constant.NotifyLimitCount = 5
	t.Cleanup(func() { constant.NotifyLimitCount = oldLimit })

	ctx := context.Background()
	allowed, err := CheckNotificationLimit(ctx, 990004, "cov-dispatch-redis")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Fatal("expected the first call to be allowed")
	}

	// The redis-path key must actually have been written (proves the Redis
	// path ran, not the in-memory one — the memory path uses a different key
	// format with no colon-prefixed "notify_limit:" namespace).
	key := "notify_limit:990004:cov-dispatch-redis:" + time.Now().Format("2006010215")
	if _, err := common.RedisGet(ctx, key); err != nil {
		t.Fatalf("expected checkRedisLimit's key to exist in Redis, got error: %v", err)
	}
}

// TestCoreAppBootCheckRedisLimit_ZeroDurationNeverBlocks_FINDING locks in a
// real latent bug at the checkRedisLimit / common.RedisIncr boundary.
//
// NOTE: NOTIFICATION_LIMIT_DURATION_MINUTE=0 used to write a TTL-less counter
// key that common.RedisIncr then refused to increment, permanently bypassing the
// notification rate limit. Covered by TestFixNotifyLimit_ZeroWindowStillLimitsRedis
// in fix_notify_limit_duration_test.go (plus the memory-path and getDuration
// siblings there), which asserts the counter really climbs, the key carries an
// expiry, and the over-limit call is rejected.
