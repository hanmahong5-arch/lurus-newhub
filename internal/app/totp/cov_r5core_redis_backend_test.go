package totp

// Covers the Redis-backed branches of MarkCodeUsed / AllowAttempt /
// RecordFailure / ClearFailures. The existing totp_test.go only exercises
// the process-local (common.RedisEnabled=false) fallback paths; this file
// wires a real miniredis instance so the common.RedisEnabled=true branches
// (SetNX replay guard, INCR/EXPIRE fail counter, DEL reset, and the
// fail-closed / fail-open error paths) are actually executed.

import (
	"context"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// r5core_withRedisBackend points common.RDB at a fresh miniredis instance and
// flips common.RedisEnabled=true for the duration of the test, restoring the
// prior globals on cleanup so other packages' tests aren't affected.
func r5core_withRedisBackend(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	prevEnabled, prevRDB := common.RedisEnabled, common.RDB
	common.RedisEnabled = true
	common.RDB = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		common.RedisEnabled, common.RDB = prevEnabled, prevRDB
		mr.Close()
	})
	return mr
}

func TestMarkCodeUsed_RedisBackend_FirstUseThenReplayBlocked(t *testing.T) {
	r5core_withRedisBackend(t)
	ctx := context.Background()

	if !MarkCodeUsed(ctx, 4001, "111111") {
		t.Fatalf("first use of a fresh code must be accepted")
	}
	if MarkCodeUsed(ctx, 4001, "111111") {
		t.Fatalf("replay of an already-used code must be rejected")
	}
	// Different user, same code text: independent keyspace.
	if !MarkCodeUsed(ctx, 4002, "111111") {
		t.Fatalf("same code under a different user id must be treated as unused")
	}
}

func TestMarkCodeUsed_RedisBackend_ErrorFailsClosed(t *testing.T) {
	mr := r5core_withRedisBackend(t)
	// Simulate a broken Redis connection instead of a real outage.
	mr.Close()

	if MarkCodeUsed(context.Background(), 4003, "222222") {
		t.Fatalf("MarkCodeUsed must fail closed (return false) when Redis errors")
	}
}

func TestAllowAttempt_RedisBackend_MissingKeyAllows(t *testing.T) {
	r5core_withRedisBackend(t)
	if !AllowAttempt(context.Background(), 5001) {
		t.Fatalf("a user with no recorded failures must be allowed to attempt")
	}
}

func TestAllowAttempt_RedisBackend_UnderAndAtLimit(t *testing.T) {
	r5core_withRedisBackend(t)
	ctx := context.Background()
	userId := 5002

	for i := 0; i < failLimit-1; i++ {
		RecordFailure(ctx, userId)
	}
	if !AllowAttempt(ctx, userId) {
		t.Fatalf("user under the fail limit (%d/%d) must still be allowed", failLimit-1, failLimit)
	}

	RecordFailure(ctx, userId) // now exactly at failLimit
	if AllowAttempt(ctx, userId) {
		t.Fatalf("user at the fail limit (%d/%d) must be blocked", failLimit, failLimit)
	}
}

func TestAllowAttempt_RedisBackend_ErrorFailsOpen(t *testing.T) {
	mr := r5core_withRedisBackend(t)
	mr.Close()

	// A broken Redis connection must not lock everyone out — the limiter is
	// documented best-effort and fails open on transport errors.
	if !AllowAttempt(context.Background(), 5003) {
		t.Fatalf("AllowAttempt must fail open (return true) when Redis errors")
	}
}

func TestRecordFailure_RedisBackend_SetsExpiryOnFirstFailure(t *testing.T) {
	mr := r5core_withRedisBackend(t)
	ctx := context.Background()
	userId := 5004

	RecordFailure(ctx, userId)

	ttl := mr.TTL(failKey(userId))
	if ttl <= 0 {
		t.Fatalf("expected the fail counter key to carry a TTL after the first failure, got %v", ttl)
	}
}

func TestRecordFailure_RedisBackend_ErrorIsNonFatal(t *testing.T) {
	mr := r5core_withRedisBackend(t)
	mr.Close()

	// Must not panic; the function only logs and returns on error.
	RecordFailure(context.Background(), 5005)
}

func TestClearFailures_RedisBackend_ResetsCounter(t *testing.T) {
	r5core_withRedisBackend(t)
	ctx := context.Background()
	userId := 5006

	RecordFailure(ctx, userId)
	RecordFailure(ctx, userId)
	if AllowAttempt(ctx, userId) == false && failLimit > 2 {
		t.Fatalf("sanity: user should still be under the limit before clearing")
	}

	ClearFailures(ctx, userId)

	if !AllowAttempt(ctx, userId) {
		t.Fatalf("after ClearFailures the user must be allowed to attempt again")
	}
}

func TestClearFailures_RedisBackend_ErrorIsNonFatal(t *testing.T) {
	mr := r5core_withRedisBackend(t)
	mr.Close()

	// Must not panic; the function only logs and returns on error.
	ClearFailures(context.Background(), 5007)
}
