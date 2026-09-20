package handler

import (
	"context"
	"fmt"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
)

// taskChargeLedger names the ledgers an async-task submission debited beyond
// the user's balance, recovered from the consume row the submission wrote.
//
// It is recovered rather than read off the task because neither tasks nor
// midjourneys carries a token_id or tenant_id column (domain/entity/task.go,
// domain/entity/midjourney.go at HEAD) — the poller that refunds a failed
// task knows the user, the channel and the amount, and nothing else about
// who paid.
type taskChargeLedger struct {
	// LogID is the submission's consume row — the row whose quota a
	// re-settlement rewrites (task_video.go).
	LogID int
	// TokenID / TokenKey address the per-key allowance the submission debited.
	TokenID  int
	TokenKey string
	// TenantID is the credit pool that was debited, resolved through the
	// token exactly the way app.debitTenantPool resolves it, so a hand-back
	// lands on the pool the debit came from. "" when the key has no tenant.
	TenantID string
	// WalletAccountID > 0 means the submission also moved platform wallet
	// money. There is no reverse RPC for that leg (O-refund), so it is
	// reported, never silently assumed reversed.
	WalletAccountID int64
}

// taskChargeLookback bounds how far back a refund looks for its submission
// row. Async tasks that are still pending after a month are not coming back.
const taskChargeLookback = 30 * 24 * time.Hour

// taskChargeCandidates bounds the recovery's query cost. Rows of the same
// shape are interchangeable for the key/pool question — what matters is that
// every candidate names the SAME key, which is what makes the recovery
// unambiguous.
const taskChargeCandidates = 50

// resolveTaskChargeLedger finds the consume row an async-task submission
// wrote for (user, channel, amount) and reads the payer off it.
//
// since bounds the search window (unix seconds). The result is reported as
// resolved ONLY when every candidate row names the same key: two keys of one
// user with identically-shaped charges make the payer genuinely unknown, and
// moving money onto a guess is worse than leaving the ledger short and saying
// so. The same is true when consume logging was off for the submission
// (common.LogConsumeEnabled=false, or the user's log_detail_level="none"):
// there is no row, so there is no payer to find.
func resolveTaskChargeLedger(userId, channelId, quota int, since int64) (taskChargeLedger, bool) {
	if userId <= 0 || quota <= 0 || repo.LOG_DB == nil {
		return taskChargeLedger{}, false
	}

	var candidates []repo.Log
	err := repo.LOG_DB.Model(&repo.Log{}).
		Select("id", "token_id").
		Where("type = ? AND user_id = ? AND channel_id = ? AND quota = ? AND token_id > 0 AND created_at >= ?",
			repo.LogTypeConsume, userId, channelId, quota, since).
		Order("id DESC").
		Limit(taskChargeCandidates).
		Find(&candidates).Error
	if err != nil || len(candidates) == 0 {
		return taskChargeLedger{}, false
	}
	tokenId := candidates[0].TokenId
	for _, row := range candidates {
		if row.TokenId != tokenId {
			return taskChargeLedger{}, false
		}
	}

	token, err := repo.GetTokenById(tokenId)
	if err != nil || token == nil {
		// The key was deleted since the submission: its allowance no longer
		// exists to credit, and its tenant is no longer knowable.
		return taskChargeLedger{}, false
	}
	return taskChargeLedger{
		LogID:           candidates[0].Id,
		TokenID:         tokenId,
		TokenKey:        token.Key,
		TenantID:        token.TenantId,
		WalletAccountID: token.IdentityAccountID,
	}, true
}

// creditTaskLedgers hands a task charge back on the two local ledgers beyond
// the user balance: the per-key allowance and the tenant credit pool. The
// third thing a submission can move — the platform wallet — is reported
// rather than reversed (see the wallet branch below). Best-effort in
// the same sense the debit is — it never fails the caller — but a leg that
// does not land is logged at the severity of a conservation break, because
// that is what it is.
//
// reason is the short pool-draw detail (the draws column is varchar(32)); the
// task's own identifier belongs in the caller's log line.
func creditTaskLedgers(ctx context.Context, ledger taskChargeLedger, quota int, reason string) {
	if quota <= 0 {
		return
	}
	//nolint:contextcheck // repo's cache refresh is detached by design, as at the sites this consolidates
	if err := repo.IncreaseTokenQuota(ledger.TokenID, ledger.TokenKey, quota); err != nil {
		common.SysError(fmt.Sprintf(
			`{"event":"task_refund_key_lost","who":"token:%d","what":"hand-back of %d (%s) failed","result":"per-key allowance NOT restored: %s"}`,
			ledger.TokenID, quota, reason, err.Error()))
	}
	if ledger.TenantID != "" {
		if err := repo.CreditPoolAdjustment(ledger.TenantID, quota, reason); err != nil {
			common.SysError(fmt.Sprintf(
				`{"event":"task_refund_pool_lost","who":"tenant:%s","what":"hand-back of %d (%s) failed","result":"pool balance NOT restored: %s"}`,
				ledger.TenantID, quota, reason, err.Error()))
		}
	}
	if ledger.WalletAccountID > 0 {
		// The submission's wallet debit stands: lurus-platform exposes
		// PreAuthorize / Settle / Release / Debit and no reverse of a settled
		// debit (O-refund). Saying so per occurrence is the honest position —
		// the alternative, silence, reads as "all four ledgers agree". The
		// counter is the same statement on /metrics, where an operator can
		// see the backlog of wallet overcharges accumulate.
		metrics.BillingTaskRefundWalletUnreversed()
		common.SysError(fmt.Sprintf(
			`{"event":"task_refund_wallet_unreversed","who":"account:%d","what":"hand-back of %d (%s) covers the local ledgers only","result":"wallet leg not reversed (O-refund): no platform refund RPC"}`,
			ledger.WalletAccountID, quota, reason))
	}
	logger.LogInfo(ctx, fmt.Sprintf("task refund reversed key %d and pool %q for %d (%s)",
		ledger.TokenID, ledger.TenantID, quota, reason))
}

// refundTaskQuota reverses an async-task charge in full.
//
// "In full" is the whole point. A task charge moves the user's spendable
// balance down, the user's cumulative used_quota up, the channel's used_quota
// up (relay/mjproxy_handler.go:243-244, relay/relay_task.go:256-257) AND —
// through app.PostConsumeQuota — the token's remain_quota and the tenant
// credit pool. Until 2026-09-01 the four refund sites (Midjourney bulk +
// single, generic async task, video task) each restored only the balance, so
// every failed task left the two used_quota counters permanently inflated:
// `quota + used_quota` drifted above the amount the account was ever funded,
// and every report derived from used_quota — dashboard spend, per-channel
// cost attribution — over-stated reality by the sum of all failures. Upstream
// New API found and fixed the same class of defect independently (#6795).
// Until cycle 13 the per-key allowance and the pool were likewise never
// handed back, which is worse than a wrong report: both are spend GATES, so
// a run of failed tasks locks a customer out of capacity they paid for.
//
// request_count is deliberately NOT reversed: the request did happen, and the
// counter means attempts, not spend. daily_used is not reversed either —
// repo.IncreaseDailyUsed rejects negatives and the counter resets on a
// UTC-day boundary, so a subtraction could land on the next day's allowance
// (see app/quota.go Phase 2).
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
	// A refund of the full charge is identified by that charge: the
	// submission's consume row carries the same amount.
	ledger, resolved := resolveTaskChargeLedger(userId, channelId, quota, time.Now().Add(-taskChargeLookback).Unix())
	if !resolved {
		common.SysError(fmt.Sprintf(
			`{"event":"task_refund_payer_unresolved","who":"user:%d","what":"refund of %d on channel %d found no single consume row","result":"balance restored; per-key allowance and tenant pool NOT restored"}`,
			userId, quota, channelId))
	}
	return refundTaskCharge(ctx, userId, channelId, quota, logContent, ledger, resolved, "task_refund")
}

// refundTaskCharge is refundTaskQuota with the payer already resolved, for
// the caller that cannot look it up by the refunded amount: a video
// re-settlement hands back the DIFFERENCE between estimate and actual, while
// the row that names the payer carries the estimate (task_video.go).
//
// resolved=false keeps the user-balance refund and skips the other two
// ledgers, which is the honest outcome when nothing names the payer.
func refundTaskCharge(ctx context.Context, userId, channelId, quota int, logContent string,
	ledger taskChargeLedger, resolved bool, reason string) bool {
	if quota == 0 {
		return false
	}
	restored := true
	// ctx is the caller's poller context, used for the log lines. The repo
	// helpers below keep the same signatures they had at the four call sites
	// this replaced — their internal cache refreshes are deliberately detached
	// fire-and-forget, so threading ctx into them would let a cancelled poll
	// abort a refund that has already moved money.
	//nolint:contextcheck // unchanged from the call sites this consolidates; the cache refresh inside is intentionally detached
	if err := repo.IncreaseUserQuota(userId, quota, false); err != nil {
		logger.LogError(ctx, "fail to increase user quota: "+err.Error())
		restored = false
	} else {
		// Only reverse the counters when the balance really went back —
		// otherwise a failed restore would understate usage on top of not
		// refunding.
		repo.UpdateUserUsedQuota(userId, -quota)
		repo.UpdateChannelUsedQuota(channelId, -quota)
		if resolved {
			creditTaskLedgers(ctx, ledger, quota, reason)
		}
	}
	// Recorded even when the restore failed, matching the behaviour of the
	// sites this replaced: the operator needs the trace either way.
	//nolint:contextcheck // same as above — RecordLog's username-cache refresh is detached by design
	repo.RecordLog(userId, repo.LogTypeSystem, logContent)
	return restored
}
