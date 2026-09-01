package handler

// task_refund_test.go — an async-task refund must reverse EVERY counter the
// submission moved, not only the spendable balance.
//
// Submission (relay/relay_task.go:256-257, relay/mjproxy_handler.go:243-244)
// moves three numbers: user.quota down, user.used_quota up, channel.used_quota
// up. Until 2026-09-01 the refund sites moved exactly one of them back, so
// `quota + used_quota` climbed above the funded amount by the total of all
// failed tasks and every used_quota-derived report over-stated spend forever.
//
// Mutation oracle: delete either UpdateUserUsedQuota or UpdateChannelUsedQuota
// from refundTaskQuota and the corresponding assertion below goes red while the
// balance assertion stays green — which is precisely the shape that let the bug
// hide (the money looked right).

import (
	"context"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

func readUserCounters(t *testing.T, ctx *V2TestContext, userID int) (quota, usedQuota, requestCount int) {
	t.Helper()
	var u repo.User
	if err := ctx.DB.First(&u, userID).Error; err != nil {
		t.Fatalf("read user %d: %v", userID, err)
	}
	return u.Quota, u.UsedQuota, u.RequestCount
}

func readChannelUsedQuota(t *testing.T, ctx *V2TestContext, channelID int) int {
	t.Helper()
	var ch repo.Channel
	if err := ctx.DB.First(&ch, channelID).Error; err != nil {
		t.Fatalf("read channel %d: %v", channelID, err)
	}
	return int(ch.UsedQuota)
}

// chargeLikeSubmission reproduces exactly what the task-submission paths do, so
// the refund is measured against a real "charged" state rather than a
// hand-built one.
func chargeLikeSubmission(t *testing.T, userID, channelID, quota int) {
	t.Helper()
	if err := repo.DecreaseUserQuota(userID, quota); err != nil {
		t.Fatalf("pre-charge DecreaseUserQuota: %v", err)
	}
	repo.UpdateUserUsedQuotaAndRequestCount(userID, quota)
	repo.UpdateChannelUsedQuota(channelID, quota)
}

func TestRefundTaskQuota_ReversesBalanceAndBothUsedQuotaCounters(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := SeedV2Channel(t, ctx, "refund-symmetry-ch")
	userID := ctx.NormalUser.Id

	startQuota, startUsed, startReqs := readUserCounters(t, ctx, userID)
	startChannelUsed := readChannelUsedQuota(t, ctx, ch.Id)

	const charge = 700
	chargeLikeSubmission(t, userID, ch.Id, charge)

	// Sanity: the charge really landed, otherwise the refund assertions below
	// would pass against a no-op.
	if q, u, _ := readUserCounters(t, ctx, userID); q != startQuota-charge || u != startUsed+charge {
		t.Fatalf("pre-charge did not land: quota=%d used=%d, want quota=%d used=%d",
			q, u, startQuota-charge, startUsed+charge)
	}

	refundTaskQuota(context.Background(), userID, ch.Id, charge, "task failed, refund")

	quota, used, reqs := readUserCounters(t, ctx, userID)
	if quota != startQuota {
		t.Errorf("balance = %d, want %d — the refund did not restore the spendable quota", quota, startQuota)
	}
	if used != startUsed {
		t.Errorf("used_quota = %d, want %d — a refund that leaves used_quota up inflates lifetime spend "+
			"on every failed task (quota+used_quota drifts above the funded amount)", used, startUsed)
	}
	if got := readChannelUsedQuota(t, ctx, ch.Id); got != startChannelUsed {
		t.Errorf("channel used_quota = %d, want %d — per-channel cost attribution keeps the refunded spend",
			got, startChannelUsed)
	}
	// The attempt happened; only the spend is reversed.
	if reqs != startReqs+1 {
		t.Errorf("request_count = %d, want %d — a refund must not un-count the attempt", reqs, startReqs+1)
	}
}

// TestRefundTaskQuota_ZeroIsNoOp pins the guard the call sites used to carry
// inline: a task that was never charged must not produce a refund log line or
// move any counter.
func TestRefundTaskQuota_ZeroIsNoOp(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := SeedV2Channel(t, ctx, "refund-zero-ch")
	userID := ctx.NormalUser.Id
	startQuota, startUsed, startReqs := readUserCounters(t, ctx, userID)

	var logsBefore int64
	ctx.DB.Model(&repo.Log{}).Where("user_id = ?", userID).Count(&logsBefore)

	if restored := refundTaskQuota(context.Background(), userID, ch.Id, 0, "should not appear"); restored {
		t.Error("refundTaskQuota reported a restore for a zero-quota task")
	}

	quota, used, reqs := readUserCounters(t, ctx, userID)
	if quota != startQuota || used != startUsed || reqs != startReqs {
		t.Errorf("zero refund moved counters: quota %d→%d used %d→%d reqs %d→%d",
			startQuota, quota, startUsed, used, startReqs, reqs)
	}
	var logsAfter int64
	ctx.DB.Model(&repo.Log{}).Where("user_id = ?", userID).Count(&logsAfter)
	if logsAfter != logsBefore {
		t.Errorf("zero refund wrote %d log row(s); a task that was never charged has nothing to compensate",
			logsAfter-logsBefore)
	}
}
