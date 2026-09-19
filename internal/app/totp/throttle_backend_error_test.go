package totp

// throttle_backend_error_test.go — L4 (cycle 12). The per-user wrong-code
// throttle is the only thing standing between a stolen password and an
// unlimited TOTP guessing run (6 digits = 1,000,000 codes, and a code stays
// valid for a whole step). Before this file, both halves of it silently
// stopped counting whenever Redis returned anything other than a value:
//
//	AllowAttempt  — `if err != nil { return true }` treated a dial failure,
//	                a timeout and a NOAUTH exactly like "this user has no
//	                recorded failures".
//	RecordFailure — logged the error and returned, so nothing was counted
//	                anywhere.
//
// Together that means a Redis outage removed the brute-force ceiling on
// verification entirely, on every replica at once. The contract these tests
// pin: redis.Nil (the key genuinely does not exist) still means "no
// failures"; any OTHER error falls back to the process-local counter, which
// is a real ceiling per replica rather than no ceiling at all.

import (
	"context"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/redis/go-redis/v9"
)

// withDeadRedisBackend points common.RDB at a port nothing listens on: every
// command returns a dial error, which is the shape of a real outage (not
// redis.Nil). MaxRetries -1 plus a short DialTimeout keeps the simulation
// from costing this package seconds per command — a closed miniredis works
// too but each command then pays the client's default retry schedule.
func withDeadRedisBackend(t *testing.T) {
	t.Helper()
	prevEnabled, prevRDB := common.RedisEnabled, common.RDB
	common.RedisEnabled = true
	common.RDB = redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1",
		DialTimeout: 100 * time.Millisecond,
		MaxRetries:  -1,
		PoolSize:    1,
	})
	t.Cleanup(func() { common.RedisEnabled, common.RDB = prevEnabled, prevRDB })
}

// recordFailuresWithMemoryBackend drives the process-local counter directly
// by turning the Redis backend off for the duration of the calls.
func recordFailuresWithMemoryBackend(t *testing.T, userID, n int) {
	t.Helper()
	prevEnabled := common.RedisEnabled
	common.RedisEnabled = false
	for i := 0; i < n; i++ {
		RecordFailure(context.Background(), userID)
	}
	common.RedisEnabled = prevEnabled
}

// TestAllowAttempt_RedisErrorConsultsMemoryCounter isolates the read half:
// the failures are already in the process-local counter, and the Redis
// backend is dead. Before the fix this returned true forever.
func TestAllowAttempt_RedisErrorConsultsMemoryCounter(t *testing.T) {
	ResetStateForTest()
	t.Cleanup(ResetStateForTest)

	const userID = 4101
	recordFailuresWithMemoryBackend(t, userID, failLimit)
	withDeadRedisBackend(t)

	if AllowAttempt(context.Background(), userID) {
		t.Fatalf("AllowAttempt = true with %d recorded failures and a dead Redis — the throttle is not enforcing", failLimit)
	}
}

// TestRecordFailure_RedisErrorFeedsMemoryCounter isolates the write half:
// the failures are recorded while Redis is dead, and the verdict is read
// back from the process-local counter. Before the fix nothing was counted.
func TestRecordFailure_RedisErrorFeedsMemoryCounter(t *testing.T) {
	ResetStateForTest()
	t.Cleanup(ResetStateForTest)

	const userID = 4102
	withDeadRedisBackend(t)
	ctx := context.Background()
	for i := 0; i < failLimit; i++ {
		RecordFailure(ctx, userID)
	}

	// Read the verdict through the memory backend so this case cannot pass
	// on AllowAttempt's error branch alone.
	prevEnabled := common.RedisEnabled
	common.RedisEnabled = false
	allowed := AllowAttempt(ctx, userID)
	common.RedisEnabled = prevEnabled

	if allowed {
		t.Fatalf("AllowAttempt = true after %d failures recorded against a dead Redis — RecordFailure dropped them", failLimit)
	}
}

// TestAllowAttempt_EndToEndWithDeadRedis is the whole loop through the same
// public API a caller uses: dead backend, failLimit wrong codes, refused.
func TestAllowAttempt_EndToEndWithDeadRedis(t *testing.T) {
	ResetStateForTest()
	t.Cleanup(ResetStateForTest)

	const userID = 4103
	withDeadRedisBackend(t)
	ctx := context.Background()

	if !AllowAttempt(ctx, userID) {
		t.Fatal("AllowAttempt = false before any failure — the fallback must not refuse a user who has done nothing")
	}
	for i := 0; i < failLimit; i++ {
		if !AllowAttempt(ctx, userID) {
			t.Fatalf("AllowAttempt = false at failure %d of %d — the budget shrank", i, failLimit)
		}
		RecordFailure(ctx, userID)
	}
	if AllowAttempt(ctx, userID) {
		t.Fatalf("AllowAttempt = true after %d failures against a dead Redis", failLimit)
	}
}

// TestAllowAttempt_MissingKeyIsNotAnError is the other direction, and the
// reason the fix keys on redis.Nil specifically: a HEALTHY Redis with no key
// for this user means no failures, and must not be answered out of a stale
// process-local counter — that would lock out a user whose failures were
// already cleared in Redis by a successful verification on another replica.
func TestAllowAttempt_MissingKeyIsNotAnError(t *testing.T) {
	ResetStateForTest()
	t.Cleanup(ResetStateForTest)

	const userID = 4104
	recordFailuresWithMemoryBackend(t, userID, failLimit)
	r5core_withRedisBackend(t) // healthy, empty

	if !AllowAttempt(context.Background(), userID) {
		t.Fatal("AllowAttempt = false with a healthy, empty Redis — a missing key means no failures, not a fallback to the local counter")
	}
}
