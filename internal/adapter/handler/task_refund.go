package handler

import (
	"context"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
)

// refundTaskQuota reverses an async-task charge in full.
//
// "In full" is the whole point. A task charge moves THREE numbers on
// submission — the user's spendable balance down, the user's cumulative
// used_quota up, and the channel's used_quota up (see
// relay/mjproxy_handler.go:243-244, relay/relay_task.go:256-257). Until
// 2026-09-01 the four refund sites (Midjourney bulk + single, generic async
// task, video task) each restored only the balance, so every failed task left
// the two used_quota counters permanently inflated: `quota + used_quota`
// drifted above the amount the account was ever funded, and every report
// derived from used_quota — dashboard spend, per-channel cost attribution —
// over-stated reality by the sum of all failures. Upstream New API found and
// fixed the same class of defect independently (#6795).
//
// request_count is deliberately NOT reversed: the request did happen, and the
// counter means attempts, not spend.
//
// This exists as one function rather than four copies because four copies is
// exactly how they drifted apart in the first place — the video path even
// carried the asymmetry inside a single if/else, where the charge branch moved
// both counters and the refund branch next to it moved neither.
// Returns whether the balance was actually restored, so a caller that has
// follow-up state to settle (the video path rewrites task.Quota to the actual
// cost) can keep gating it on success exactly as before.
func refundTaskQuota(ctx context.Context, userId, channelId, quota int, logContent string) bool {
	if quota == 0 {
		return false
	}
	restored := true
	if err := repo.IncreaseUserQuota(userId, quota, false); err != nil {
		logger.LogError(ctx, "fail to increase user quota: "+err.Error())
		restored = false
	} else {
		// Only reverse the counters when the balance really went back —
		// otherwise a failed restore would understate usage on top of not
		// refunding.
		repo.UpdateUserUsedQuota(userId, -quota)
		repo.UpdateChannelUsedQuota(channelId, -quota)
	}
	// Recorded even when the restore failed, matching the behaviour of the
	// sites this replaced: the operator needs the trace either way.
	repo.RecordLog(userId, repo.LogTypeSystem, logContent)
	return restored
}
