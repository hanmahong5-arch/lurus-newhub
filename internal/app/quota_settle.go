package app

// quota_settle.go — Phase 5 of PostConsumeQuota: the platform wallet leg.
// Moved out of quota.go as-is in cycle 18 L2 (the file sat exactly on its
// size ceiling); the only behaviour that changed with the move is that the
// two "hold handled" exits — settleOrPark and abandonPreAuth — now own the
// clearing of PlatformPreAuthID, and a settle that fails live AND cannot be
// parked is counted (noteMoneyLost) instead of only logged.

import (
	"context"
	"fmt"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/currency"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/google/uuid"
)

// settlePlatformWallet moves this request's money on the platform wallet:
// settle (or release) the pre-auth hold when there is one, debit directly
// when there is not, release a hold the request turned out not to need.
// localQuotaConsistent/advisory decide whether a request whose local ledger
// write failed may still be charged — see PostConsumeQuota's Phase 3.
func settlePlatformWallet(relayInfo *relaycommon.RelayInfo, totalQuota, preConsumedQuota int, localQuotaConsistent, advisory bool) {
	if relayInfo.IdentityAccountID > 0 && totalQuota > 0 {
		accountID := relayInfo.IdentityAccountID
		amountLB := currency.QuotaToCNY(totalQuota)
		warnZeroWalletAmount(accountID, totalQuota, amountLB)

		if relayInfo.PlatformPreAuthID > 0 {
			preAuthID := relayInfo.PlatformPreAuthID
			charged := false
			if !localQuotaConsistent && !advisory {
				// Local quota is inconsistent — release pre-auth instead of settling,
				// to avoid charging the wallet for a request that wasn't properly recorded locally.
				abandonPreAuth(relayInfo, "local quota inconsistent")
			} else {
				// Advisory mode settles even when the local shadow write was
				// inconsistent: the platform wallet is the ledger of record and
				// the meter loss was already counted above — dropping revenue
				// over a shadow-bookkeeping failure would invert the hierarchy.
				settleOrPark(relayInfo, accountID, amountLB)
				charged = true
			}

			// Report usage for VIP accumulation (async, non-critical)
			AsyncGo(func() {
				rptCtx, rptCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer rptCancel()
				common.ReportLLMUsageGRPC(rptCtx, accountID, amountLB)
				reportQuotaThreshold(rptCtx, relayInfo, totalQuota)
				if charged {
					// Track A mirror metering: one usage_events row per charged
					// relay, joined against wallet_transactions by the daily
					// drift reconciliation. Keyed on the pre-auth id so a
					// re-report is deduped platform-side.
					mirrorUsageEvent(rptCtx, relayInfo, accountID, totalQuota, amountLB,
						fmt.Sprintf("llm-relay:settle:%d", preAuthID), preAuthID)
				}
			})
		} else {
			// Legacy path: fire-and-forget debit (no pre-auth).
			//
			// Gated on the SAME principle as the pre-auth branch above: when the
			// local settlement is inconsistent and we are not in advisory mode,
			// the wallet must not be charged for a request that was not properly
			// recorded locally. Up there the corrective action is "release
			// instead of settle"; here there is no freeze to release, so the
			// equivalent action is simply not to charge. Before this gate the
			// debit fired unconditionally, so the exact case the pre-auth branch
			// refuses to charge WAS charged for any account that happened to
			// have no pre-auth (flag-off windows, high-balance skip, degraded
			// admit) — the same double-debit risk, inconsistently applied.
			charge := localQuotaConsistent || advisory
			if charge {
				relayInfo.WalletChargeCNY4 = currency.CNYToUnits4(amountLB)
			} else {
				// Same severity/shape as the local-inconsistency logs above
				// (common.SysLog) so the skipped revenue is observable rather
				// than silently dropped.
				common.SysLog(fmt.Sprintf("legacy wallet debit skipped, local quota inconsistent (non-advisory): accountID=%d, amount=%.4f LB, userId=%d",
					accountID, amountLB, relayInfo.UserId))
			}
			refID := "llm-usage:" + uuid.NewString()
			AsyncGo(func() {
				debitCtx, debitCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer debitCancel()
				if charge {
					desc := fmt.Sprintf("relay userId=%d", relayInfo.UserId)
					if _, debitErr := debitWalletGRPC(debitCtx, accountID, amountLB, "llm_usage",
						desc, sourceProductOf(relayInfo), refID); debitErr != nil {
						enqueueFailedLegacyDebit(accountID, amountLB, desc, sourceProductOf(relayInfo), refID, debitErr)
					}
				}
				// Usage reporting stays unconditional, mirroring the pre-auth
				// branch: it runs there on the release path too, and it moves no
				// money.
				common.ReportLLMUsageGRPC(debitCtx, accountID, amountLB)
				reportQuotaThreshold(debitCtx, relayInfo, totalQuota)
				if charge {
					// Mirror the legacy debit too (shared refID joins the pair):
					// wallet>usage here would hide double charges from the drift
					// SQL. Keyed on `charge` for the same reason the pre-auth
					// branch keys its mirror on `charged` — mirroring a debit
					// that never happened invents a charge in the drift data.
					mirrorUsageEvent(debitCtx, relayInfo, accountID, totalQuota, amountLB, refID, 0)
				}
			})
		}
	} else if relayInfo.IdentityAccountID > 0 && totalQuota <= 0 && preConsumedQuota > 0 && relayInfo.PlatformPreAuthID > 0 {
		// Zero-usage settlement (e.g. an abnormal stream end that resolves to
		// no billable tokens): totalQuota <= 0 means the branch above never
		// runs, so without this arm the pre-auth froze above never gets
		// settled OR released — it just sits until the platform-side TTL
		// (PreAuthHoldTTL) auto-expires it. There is nothing to charge here,
		// so the only correct action is release, mirroring the
		// local-inconsistency release path above.
		//
		// preConsumedQuota > 0 pins this to the FINAL settlement call — the
		// only caller shape that passes FinalPreConsumedQuota through
		// (quota.go PostConsume paths, relay/compatible_handler.go). The
		// two other callers pass 0 and must not reach here: the realtime
		// WSS path (PreWssConsumeQuota) calls this once per usage event, and
		// a zero-token event mid-session must not release a freeze that the
		// next positive event is meant to settle — otherwise that event falls
		// through to the no-pre-auth debit path, which has no outbox; and
		// ReturnPreConsumedQuota passes a negative refund and does its own
		// abandonPreAuth right after.
		abandonPreAuth(relayInfo, "zero-usage settlement")
	}
}

// settleOrPark is one of the hold's two exits (abandonPreAuth is the other):
// charge the hold for what the request actually cost, or park the settle in
// the outbox when the platform cannot be reached now. The hold counts as
// handled either way and the id is cleared, so a later cleanup cannot
// release what is being settled. Only when the outbox ALSO fails is the
// money counted as lost — nothing in this process will try again, and the
// hold expires at PreAuthHoldTTL with the usage unbilled.
func settleOrPark(relayInfo *relaycommon.RelayInfo, accountID int64, amountLB float64) {
	preAuthID := relayInfo.PlatformPreAuthID
	settleCtx, settleCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, settleErr := settleWithBreaker(settleCtx, preAuthID, amountLB)
	settleCancel()
	relayInfo.WalletChargeCNY4 = currency.CNYToUnits4(amountLB)

	if settleErr != nil {
		metrics.BillingSettleTotal.WithLabelValues("error").Inc()
		common.SysLog(fmt.Sprintf("settle pre-auth %d failed, enqueuing: %s", preAuthID, settleErr.Error()))
		if enqErr := EnqueueSettle(accountID, preAuthID, amountLB); enqErr != nil {
			noteMoneyLost("settle", accountID, amountLB,
				"preauth_id", preAuthID, "err", settleErr, "outbox_err", enqErr)
		}
	} else {
		metrics.BillingSettleTotal.WithLabelValues("success").Inc()
		// This settlement leg never observed billing_debit_amount_cny —
		// only the direct DebitWalletGRPC call sites did, so a request
		// that paid through the pre-auth branch (the pre-auth → settle
		// path) moved real money with zero histogram trace.
		metrics.RecordBillingDebit(relayInfo.SourceProduct, "settle", amountLB)
		// Invalidate cached balance so next request gets fresh data
		common.InvalidateCachedWalletBalance(accountID)
	}
	relayInfo.PlatformPreAuthID = 0
}
