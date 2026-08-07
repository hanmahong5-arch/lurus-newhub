package app

// cov_core-app-boot_usage_milestone_test.go — coverage for
// CheckAndPublishUsageMilestone's real-Redis path and claimMilestone (both
// left mostly/fully uncovered: usage_milestone_test.go only exercises the
// pure crossedMilestones helper and the Redis-disabled no-op guard). Uses
// withMiniRedisTPM from business_tpm_test.go (same package). NATS is not
// configured in this test binary, so hubnats.PublishUsageMilestone's Get()
// returns nil and it no-ops — that's fine, we assert on the Redis dedup
// side effects claimMilestone itself owns.

import (
	"context"
	"testing"
)

// TestCoreAppBootClaimMilestone_FirstCallerWinsSecondIsDeduped drives the
// real SETNX dedup primitive directly: the first claim for a given
// (user, threshold) must succeed, and a second claim for the exact same pair
// must be rejected (the at-most-once publish guarantee).
func TestCoreAppBootClaimMilestone_FirstCallerWinsSecondIsDeduped(t *testing.T) {
	withMiniRedisTPM(t)
	ctx := context.Background()

	first := claimMilestone(ctx, 771001, 1000)
	if !first {
		t.Fatal("expected the first claim for a fresh (user, threshold) pair to win")
	}
	second := claimMilestone(ctx, 771001, 1000)
	if second {
		t.Fatal("expected a second claim for the same (user, threshold) pair to be rejected")
	}

	// A different threshold for the same user must be independent.
	third := claimMilestone(ctx, 771001, 10000)
	if !third {
		t.Fatal("expected a claim for a different threshold to succeed independently")
	}
}

// TestCoreAppBootCheckAndPublishUsageMilestone_CrossesFirstTierOnce drives
// the full production entry point against real (miniredis) Redis: a fresh
// user sending exactly 1000 tokens must cross the first_1k tier exactly
// once, and the cumulative counter (llm:tokens:<id>) must reflect the
// INCRBY.
func TestCoreAppBootCheckAndPublishUsageMilestone_CrossesFirstTierOnce(t *testing.T) {
	mr := withMiniRedisTPM(t)
	ctx := context.Background()
	userID := 771002

	CheckAndPublishUsageMilestone(ctx, userID, 1000)

	cumKey := "llm:tokens:771002"
	got, err := mr.Get(cumKey)
	if err != nil {
		t.Fatalf("expected cumulative counter key to exist: %v", err)
	}
	if got != "1000" {
		t.Errorf("cumulative counter = %q, want %q", got, "1000")
	}

	// The dedup key for the first_1k tier must now be claimed — a direct
	// claimMilestone call for the same threshold must report already-claimed.
	if claimMilestone(ctx, userID, 1000) {
		t.Error("expected the first_1k dedup key to already be claimed by CheckAndPublishUsageMilestone")
	}
}

// TestCoreAppBootCheckAndPublishUsageMilestone_TwoRequestsCrossOnce verifies
// that two separate requests that jointly cross a tier (neither alone does)
// still result in the dedup key being claimed by the second call — proving
// the crossing walk gates on the cumulative prevTotal/newTotal window rather
// than firing (or missing) per individual call.
func TestCoreAppBootCheckAndPublishUsageMilestone_TwoRequestsCrossOnce(t *testing.T) {
	withMiniRedisTPM(t)
	ctx := context.Background()
	userID := 771004

	CheckAndPublishUsageMilestone(ctx, userID, 600) // 0 -> 600: under 1000, no crossing
	CheckAndPublishUsageMilestone(ctx, userID, 500) // 600 -> 1100: crosses first_1k

	if claimMilestone(ctx, userID, 1000) {
		t.Fatal("expected first_1k to already be claimed after the cumulative total crossed it")
	}
}

// TestCoreAppBootCheckAndPublishUsageMilestone_MultiTierSingleRequest
// verifies a single large request that jumps past multiple tiers claims all
// of them (ascending order per the source comment), not just the highest.
func TestCoreAppBootCheckAndPublishUsageMilestone_MultiTierSingleRequest(t *testing.T) {
	withMiniRedisTPM(t)
	ctx := context.Background()
	userID := 771005

	CheckAndPublishUsageMilestone(ctx, userID, 15_000) // 0 -> 15000: crosses first_1k AND first_10k

	if claimMilestone(ctx, userID, 1000) {
		t.Error("expected first_1k to be claimed after a single request crossing multiple tiers")
	}
	if claimMilestone(ctx, userID, 10_000) {
		t.Error("expected first_10k to be claimed after a single request crossing multiple tiers")
	}
	// Not yet reached.
	if !claimMilestone(ctx, userID, 100_000) {
		t.Error("expected first_100k to remain unclaimed (not yet reached)")
	}
}

// TestCoreAppBootCheckAndPublishUsageMilestone_NoOpGuards locks the
// early-return guards (userID<=0, totalTokens<=0) so they can never silently
// start writing garbage keys like "llm:tokens:0" or "llm:tokens:-5".
func TestCoreAppBootCheckAndPublishUsageMilestone_NoOpGuards(t *testing.T) {
	mr := withMiniRedisTPM(t)
	ctx := context.Background()

	CheckAndPublishUsageMilestone(ctx, 0, 1000)
	CheckAndPublishUsageMilestone(ctx, -5, 1000)
	CheckAndPublishUsageMilestone(ctx, 771006, 0)
	CheckAndPublishUsageMilestone(ctx, 771006, -100)

	for _, key := range []string{"llm:tokens:0", "llm:tokens:-5", "llm:tokens:771006"} {
		if mr.Exists(key) {
			t.Errorf("expected no cumulative counter key %q to be written by any no-op call", key)
		}
	}
}
