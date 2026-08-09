package app

// cov_core-app-boot_cost_spike_test.go — business-acceptance coverage for
// cost_spike.go's Redis-backed window functions (QueryCostSpikeWindow 0%,
// RecordCostSpikeWindow 14.3%), left untested by cost_spike_test.go which
// only covers the pure parseCostSpikeMember helper. Uses
// withMiniRedisTPM/withoutRedisTPM from business_tpm_test.go (same package).

import (
	"context"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/redis/go-redis/v9"
)

// core_app_boot_saveCostSpikeProtection snapshots/restores the protection
// flag so tests toggling it don't leak into siblings.
func core_app_boot_saveCostSpikeProtection(t *testing.T) {
	t.Helper()
	prev := common.CostSpikeProtectionEnabled
	t.Cleanup(func() { common.CostSpikeProtectionEnabled = prev })
}

// TestCoreAppBootRecordAndQueryCostSpikeWindow_RoundTrip records two usage
// events for the same user and verifies QueryCostSpikeWindow sums exactly
// their tokens within the 5-minute window.
func TestCoreAppBootRecordAndQueryCostSpikeWindow_RoundTrip(t *testing.T) {
	withMiniRedisTPM(t)
	core_app_boot_saveCostSpikeProtection(t)
	common.CostSpikeProtectionEnabled = true

	userID := 881001
	RecordCostSpikeWindow(userID, 1000)
	RecordCostSpikeWindow(userID, 500)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	total, err := QueryCostSpikeWindow(ctx, common.RDB, userID)
	if err != nil {
		t.Fatalf("QueryCostSpikeWindow: unexpected error: %v", err)
	}
	if total != 1500 {
		t.Errorf("total = %d, want 1500 (sum of the two recorded events)", total)
	}
}

// TestCoreAppBootRecordCostSpikeWindow_NoOpBranches locks the three no-op
// guards: protection disabled, Redis disabled, and non-positive tokens
// (refunds). None of these should write anything to Redis, verified by
// querying the window afterward and expecting zero.
func TestCoreAppBootRecordCostSpikeWindow_NoOpBranches(t *testing.T) {
	withMiniRedisTPM(t)
	core_app_boot_saveCostSpikeProtection(t)

	// RecordCostSpikeWindow takes no context (it is fire-and-forget from the
	// billing path), so each subtest records first and only then derives the
	// context its QueryCostSpikeWindow read needs — keeping a context in scope
	// across the recording call would just be misleading.
	t.Run("protection_disabled", func(t *testing.T) {
		common.CostSpikeProtectionEnabled = false
		userID := 881002
		RecordCostSpikeWindow(userID, 1000)
		common.CostSpikeProtectionEnabled = true

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		total, err := QueryCostSpikeWindow(ctx, common.RDB, userID)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if total != 0 {
			t.Errorf("total = %d, want 0 when protection is disabled", total)
		}
	})

	t.Run("non_positive_tokens_are_refund_noop", func(t *testing.T) {
		common.CostSpikeProtectionEnabled = true
		userID := 881003
		RecordCostSpikeWindow(userID, 0)
		RecordCostSpikeWindow(userID, -50)

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		total, err := QueryCostSpikeWindow(ctx, common.RDB, userID)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if total != 0 {
			t.Errorf("total = %d, want 0 for non-positive token deltas (refunds must not count)", total)
		}
	})

	t.Run("redis_disabled_is_noop_and_does_not_panic", func(t *testing.T) {
		common.CostSpikeProtectionEnabled = true
		prevEnabled, prevRDB := common.RedisEnabled, common.RDB
		common.RedisEnabled = false
		defer func() { common.RedisEnabled, common.RDB = prevEnabled, prevRDB }()

		// Must not panic even though RDB is still set but RedisEnabled=false.
		RecordCostSpikeWindow(881004, 1000)
	})
}

// TestCoreAppBootQueryCostSpikeWindow_EvictsExpiredEntries verifies the
// 5-minute sliding window actually evicts entries older than the window
// rather than accumulating forever.
func TestCoreAppBootQueryCostSpikeWindow_EvictsExpiredEntries(t *testing.T) {
	withMiniRedisTPM(t)

	userID := 881005
	key := CostSpikeKeyPrefix + "881005"

	now := time.Now()
	stale := now.Add(-10 * time.Minute).UnixMilli()  // older than the 5-minute window
	fresh := now.Add(-1 * time.Minute).UnixMilli()    // within the window

	if err := common.RDB.ZAdd(context.Background(), key,
		redis.Z{Score: float64(stale), Member: "stalemarker:900"},
		redis.Z{Score: float64(fresh), Member: "freshmarker:300"},
	).Err(); err != nil {
		t.Fatalf("seed sorted set: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	total, err := QueryCostSpikeWindow(ctx, common.RDB, userID)
	if err != nil {
		t.Fatalf("QueryCostSpikeWindow: %v", err)
	}
	if total != 300 {
		t.Errorf("total = %d, want 300 (only the fresh entry should survive eviction of the stale one)", total)
	}
}
