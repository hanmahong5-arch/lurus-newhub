package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/currency"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ReturnPreConsumedQuota refunds local quota and releases platform pre-auth
// when a relay request fails after pre-consumption. Must be safe to call
// multiple times (idempotent on relayInfo state).
func ReturnPreConsumedQuota(c *gin.Context, relayInfo *relaycommon.RelayInfo) {
	// Refund local quota
	if relayInfo.FinalPreConsumedQuota != 0 {
		logger.LogInfo(c, fmt.Sprintf("refunding pre-consumed quota %s for user %d",
			logger.FormatQuota(relayInfo.FinalPreConsumedQuota), relayInfo.UserId))
		err := PostConsumeQuota(relayInfo, -relayInfo.FinalPreConsumedQuota, 0, false)
		if err != nil {
			common.SysError(fmt.Sprintf("failed to refund local quota: userId=%d, amount=%d, err=%s",
				relayInfo.UserId, relayInfo.FinalPreConsumedQuota, err.Error()))
		}
	}

	// Release platform wallet freeze — every pre-auth MUST be either settled or released.
	abandonPreAuth(relayInfo, "request failed after pre-consume")
}

// releasePlatformPreAuth releases a platform pre-auth with retry-to-outbox fallback.
// Safe to call when PlatformPreAuthID == 0 (no-op).
func releasePlatformPreAuth(relayInfo *relaycommon.RelayInfo) {
	preAuthID := relayInfo.PlatformPreAuthID
	if preAuthID <= 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := common.ReleaseWithBreaker(ctx, preAuthID); err != nil {
		common.SysLog(fmt.Sprintf("release pre-auth %d failed, enqueuing outbox: %s", preAuthID, err.Error()))
		if enqErr := EnqueueRelease(relayInfo.IdentityAccountID, preAuthID); enqErr != nil {
			// Both release and outbox failed — the platform's PreAuthHoldTTL
			// sweep is the only thing left that will unfreeze the balance.
			noteMoneyLost("preauth_release", relayInfo.IdentityAccountID, 0,
				"preauth_id", preAuthID, "err", err, "outbox_err", enqErr)
		}
	}
}

// abandonPreAuth is one of the hold's two exits (settleOrPark is the other):
// release it — or park the release in the outbox — and clear the id so no
// later cleanup can touch the same hold twice. No-op without a hold. Every
// caller used to hand-copy the release and the clear, and
// ReturnPreConsumedQuota kept the id "for the logs" against the rest; the
// reason is logged here instead, so the id can go everywhere.
func abandonPreAuth(relayInfo *relaycommon.RelayInfo, reason string) {
	preAuthID := relayInfo.PlatformPreAuthID
	if preAuthID <= 0 {
		return
	}
	releasePlatformPreAuth(relayInfo)
	relayInfo.PlatformPreAuthID = 0
	common.SysLog(fmt.Sprintf("pre-auth %d abandoned (%s): accountID=%d, userId=%d",
		preAuthID, reason, relayInfo.IdentityAccountID, relayInfo.UserId))
}

// insufficientUserQuotaError is the local ledger's 402, the same shape from
// the fast pre-check and from the atomic debit that out-raced it: SkipRetry
// (another channel costs the same), no error-log row (an empty wallet is not
// an incident), the top-up link as the remedy. ASCII amounts because the
// display-currency formatter puts a fullwidth sign on the wire.
func insufficientUserQuotaError(available, required int) *types.NewAPIError {
	return types.NewErrorWithStatusCode(
		fmt.Errorf("insufficient quota: available %s, required %s",
			logger.FormatQuotaASCII(available), logger.FormatQuotaASCII(required)),
		types.ErrorCodeInsufficientUserQuota, http.StatusPaymentRequired,
		types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog(),
		types.ErrOptionWithTopupURL())
}

// rollbackTokenFreeze undoes PreConsumeTokenQuota's per-key debit when a
// later gate refuses the request, so an aborted request never strands it.
func rollbackTokenFreeze(relayInfo *relaycommon.RelayInfo, quota int) {
	if err := repo.IncreaseTokenQuota(relayInfo.TokenId, relayInfo.TokenKey, quota); err != nil {
		common.SysError(fmt.Sprintf("token freeze rollback failed: tokenId=%d, quota=%d, err=%s",
			relayInfo.TokenId, quota, err.Error()))
	}
}

// PreConsumeQuota validates the user can afford the request and pre-deducts quota.
//
// When unified billing is enabled (BILLING_UNIFIED_ENABLED=true) and the token
// is linked to a platform account (IdentityAccountID > 0), this also freezes
// the estimated cost in the platform wallet via PreAuthorize. On any failure
// after a successful pre-auth, the caller MUST call ReturnPreConsumedQuota to
// release the frozen wallet balance.
func PreConsumeQuota(c *gin.Context, preConsumedQuota int, relayInfo *relaycommon.RelayInfo) *types.NewAPIError {
	// Guard: don't re-enter pre-auth on relay retry (preAuthID already set from first attempt)
	if relayInfo.PlatformPreAuthID > 0 {
		// Already pre-authorized — skip platform call, continue to local quota check
		relayInfo.PlatformGoverned = true
		logger.LogInfo(c, fmt.Sprintf("skipping re-entry PreAuthorize, existing preAuthID=%d", relayInfo.PlatformPreAuthID))
	} else if common.BillingUnifiedEnabled() && relayInfo.IdentityAccountID > 0 && preConsumedQuota > 0 {
		if apiErr := platformPreAuthorize(c, preConsumedQuota, relayInfo); apiErr != nil {
			return apiErr
		}
	}

	// Track A: with LOCAL_LEDGER_ADVISORY on, the local user-balance ledger is
	// shadow bookkeeping for requests the platform wallet ADMITTED (governed).
	// Local writes still happen below (they feed drift reconciliation); only
	// the user-balance 402 and local write FAILURES stop blocking. Ungoverned
	// traffic (unlinked users, flag-off windows) keeps the full local gate.
	advisory := common.LocalLedgerAdvisory() && relayInfo.PlatformGoverned
	// Per-tenant variant (tenants.wallet_authoritative, migration 046): the
	// platform wallet is the only balance gate for this tenant, so a governed
	// request is admitted exactly as under global advisory mode. Ungoverned
	// requests never qualify — they were not admitted by the wallet.
	if !advisory && relayInfo.PlatformGoverned && repo.TenantWalletAuthoritative(c.GetString("tenant_id")) {
		advisory = true
		relayInfo.WalletAuthoritative = true
	}

	// Provisioned keys (handler/provisioning.go:110) are tenant-scoped and are
	// minted with UserId=0 by design — there is no user row, hence no user
	// balance to read or pre-deduct. repo.GetUserQuota(0) matches no row and
	// answers 0 WITHOUT an error, so the gate below used to 402 every single
	// provisioned relay. Their money is the token's own quota plus the tenant
	// credit pool; both of those legs still run in full below.
	provisioned := relayInfo.UserId == 0

	// Local quota validation (always runs for user-owned tokens — backward
	// compat + defense in depth)
	userQuota := 0
	if !provisioned {
		var err error
		userQuota, err = repo.GetUserQuota(relayInfo.UserId, false)
		if err != nil {
			return types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}

		if userQuota <= 0 || userQuota-preConsumedQuota < 0 {
			if advisory {
				// Platform wallet already vouched for this request — the local
				// shadow balance disagreeing is exactly the drift the advisory
				// rollout measures, not a reason to refuse paid-for service.
				metrics.BillingAdvisoryBypassTotal.WithLabelValues("user_balance_402").Inc()
				logger.LogInfo(c, fmt.Sprintf("advisory: local balance would 402 (available %s, required %s) — platform-governed, continuing",
					logger.FormatQuota(userQuota), logger.FormatQuota(preConsumedQuota)))
			} else {
				abandonPreAuth(relayInfo, "local quota insufficient")
				return insufficientUserQuotaError(userQuota, preConsumedQuota)
			}
		}
	}

	// Tenant monthly quota enforcement (runs after user-level check).
	if apiErr := enforceTenantQuota(c.GetString("tenant_id"), preConsumedQuota); apiErr != nil {
		abandonPreAuth(relayInfo, "tenant quota exceeded")
		return apiErr
	}
	// Project monthly budget (migration 043): the token's cost-attribution
	// project may carry a cap; untagged tokens skip this in one comparison.
	if apiErr := enforceProjectBudget(c.GetString("tenant_id"), relayInfo.ProjectId, preConsumedQuota); apiErr != nil {
		abandonPreAuth(relayInfo, "project budget exceeded")
		return apiErr
	}

	// Trust optimization: skip local pre-deduction when balance is high enough
	trustQuota := common.GetTrustQuota()
	relayInfo.UserQuota = userQuota

	if userQuota > trustQuota {
		if relayInfo.TokenUnlimited || c.GetInt("token_quota") > trustQuota {
			preConsumedQuota = 0
		}
	}

	if preConsumedQuota > 0 {
		if err := PreConsumeTokenQuota(relayInfo, preConsumedQuota); err != nil {
			// The per-key cap is the user's OWN spending limit, not ledger
			// state — it stays enforced even in advisory mode. Only shadow
			// WRITE failures (DB errors) get the log-and-continue treatment.
			if advisory && !errors.Is(err, ErrTokenQuotaInsufficient) {
				metrics.BillingAdvisoryBypassTotal.WithLabelValues("pre_deduct").Inc()
				common.SysLog(fmt.Sprintf("advisory: token pre-deduct failed, continuing without local freeze: userId=%d, quota=%d, err=%s",
					relayInfo.UserId, preConsumedQuota, err.Error()))
				preConsumedQuota = 0
			} else {
				abandonPreAuth(relayInfo, "token pre-deduct refused")
				if errors.Is(err, ErrTokenQuotaInsufficient) {
					// Per-TOKEN spending cap (quota.go:645-649 — "not ledger
					// state"), same remedy as the TokenAuth 402
					// (middleware/auth.go): fix the token's own remain_quota
					// or set it unlimited, not a wallet top-up.
					remainQuota := 0
					if tok, gErr := repo.GetTokenByKey(relayInfo.TokenKey, false); gErr == nil && tok != nil {
						remainQuota = tok.RemainQuota
					}
					return types.NewErrorWithStatusCode(err, types.ErrorCodeTokenQuotaExhausted,
						http.StatusPaymentRequired, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog(),
						types.ErrOptionWithTokenQuotaHint(remainQuota))
				}
				// A genuine DB/write failure here (not the per-key cap) is
				// neither a token-cap nor a user-balance rejection: it is our
				// database failing to write. It used to answer 402 with code
				// pre_consume_token_quota_failed, which RelayErrorType buckets
				// as "insufficient_quota" — so a persistence outage was
				// indistinguishable, in the customer's response AND on the
				// operator's dashboard, from "this customer ran out of money".
				//
				// The same function already handles the identical situation
				// correctly one branch down (the post-check UPDATE failure at
				// ErrorCodeUpdateDataError), so this follows that precedent:
				// 500, "internal" in RelayErrorType, SkipRetry because retrying
				// a broken write on another channel cannot help.
				//
				// NoRecordErrorLog is deliberately dropped. It is right for a
				// 402 — a customer being out of credit is not an incident and
				// would flood the error log — and wrong for an internal fault,
				// which is precisely the thing an operator needs a log row for.
				//
				// types.ErrorCodePreConsumeTokenQuotaFailed and its
				// RelayErrorType mapping stay: app/channel.go still matches the
				// literal "pre_consume_token_quota_failed" when parsing
				// responses relayed back from older downstream instances.
				return types.NewErrorWithStatusCode(err, types.ErrorCodeUpdateDataError,
					http.StatusInternalServerError, types.ErrOptionWithSkipRetry())
			}
		} else if !provisioned {
			// Tenant-scoped keys stop here: the token debit above IS their whole
			// local pre-deduction. Both branches below operate on a user row that
			// does not exist for them — and the non-advisory one would answer
			// ok=false (0 rows matched) and 402 every provisioned relay.
			if advisory {
				// Advisory shadow ledger: keep the UNCONDITIONAL user debit so the
				// shadow balance can still go negative (that drift is exactly what the
				// rollout measures) — never blocked, never 402. Only a real DB write
				// error gets the log-and-continue treatment. Byte-identical to the
				// behavior before the atomic pre-consume gate.
				if err := repo.DecreaseUserQuota(relayInfo.UserId, preConsumedQuota); err != nil {
					// Roll the token freeze back so the shadow ledger stays
					// consistent, then continue without a local freeze.
					rollbackTokenFreeze(relayInfo, preConsumedQuota)
					metrics.BillingAdvisoryBypassTotal.WithLabelValues("pre_deduct").Inc()
					common.SysLog(fmt.Sprintf("advisory: user pre-deduct failed, continuing without local freeze: userId=%d, quota=%d, err=%s",
						relayInfo.UserId, preConsumedQuota, err.Error()))
					preConsumedQuota = 0
				}
			} else {
				// Non-advisory: atomic conditional debit closes the user-gate TOCTOU.
				// The userQuota<=0 / userQuota-preConsumedQuota<0 fast pre-check above
				// stays as a cheap short-circuit, but under concurrency it can pass on
				// a balance another racing request has since drained — the atomic
				// UPDATE is the backstop that keeps quota from going negative.
				ok, err := repo.DecreaseUserQuotaIfEnough(relayInfo.UserId, preConsumedQuota)
				if err != nil || !ok {
					// The token was already atomically debited by PreConsumeTokenQuota
					// above — roll it back so an aborted request never strands a
					// per-key debit.
					rollbackTokenFreeze(relayInfo, preConsumedQuota)
					abandonPreAuth(relayInfo, "user pre-deduct refused")
					if err != nil {
						// Real DB error — same error code/path as before.
						return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
					}
					// ok == false: balance out-raced the pre-check → the same 402 the
					// fast path returns for insufficient local quota.
					return insufficientUserQuotaError(userQuota, preConsumedQuota)
				}
			}
		}
	}

	relayInfo.FinalPreConsumedQuota = preConsumedQuota
	return nil
}

// PreAuthHoldTTL is how long the platform keeps a pre-auth hold settleable.
// It was 300s. The platform's sweep marks a hold expired once it passes its
// deadline, and settling an expired hold is refused, so every request longer
// than that (the concurrency lease allows 1800s) and every settle the outbox
// retried later was never billed. The hold has to outlive the longest request
// plus the outbox retry horizon; middleware's lease test pins that sum.
const PreAuthHoldTTL = time.Hour

// billingUnavailableRetryAfter is what the 503 below tells the SDK to wait.
// Shorter than the breaker's 15s cooldown on purpose: the first failures of
// an outage land here BEFORE the breaker opens, and a transient blip is
// usually over well within that.
const billingUnavailableRetryAfter = 5 * time.Second

// preAuthorizeWithBreaker is the platform freeze call. A var (same seam
// convention as AsyncGo) because in a test binary the real call has no
// platform to reach and burns the identity budget before failing — which
// leaves the success path below, and the cache warm-up that hangs off it,
// otherwise unreachable from tests.
var preAuthorizeWithBreaker = common.PreAuthorizeWithBreaker

// preAuthFailure maps a failed platform freeze to what the customer is owed.
// Three shapes, and only the first is about their money:
//   - the platform said insufficient_balance: 402, top-up link, no error-log
//     row (an empty wallet is not an incident);
//   - the platform refused the ACCOUNT for another reason (wallet_frozen,
//     account_suspended…): still the payer's problem, so 402, but named
//     after the platform's reason, no top-up link (it would not help), and
//     an error-log row so support can find it;
//   - the platform could not be asked (timeout, breaker open or probing,
//     unconfigured) and the degrade path declined: OUR outage — 503 with
//     Retry-After, never a top-up prompt, error-log row.
//
// Every shape used to take the first arm's 402. A funded customer was told
// to top up on our timeout, their SDK did not retry (402 is terminal), no
// error-log row was written, and relay_errors_total filed the outage under
// insufficient_quota. The breaker needs BillingBreakerDefaultThreshold
// consecutive failures before the degrade path is even admissible, so the
// first failures of every outage landed exactly here.
//
// SkipRetry on all three: another channel costs the same money.
func preAuthFailure(err error) *types.NewAPIError {
	var rejected *common.PlatformRejectedError
	switch {
	case errors.Is(err, common.ErrInsufficientBalance):
		return types.NewErrorWithStatusCode(errors.New("insufficient wallet balance"),
			types.ErrorCodeInsufficientUserQuota, http.StatusPaymentRequired,
			types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog(),
			types.ErrOptionWithTopupURL())
	case errors.As(err, &rejected):
		return types.NewErrorWithStatusCode(
			fmt.Errorf("billing service rejected this request: %s", rejected.Reason),
			types.ErrorCodeInsufficientUserQuota, http.StatusPaymentRequired,
			types.ErrOptionWithSkipRetry())
	default:
		apiErr := types.NewErrorWithStatusCode(fmt.Errorf("billing service unavailable: %w", err),
			types.ErrorCodeBillingUnavailable, http.StatusServiceUnavailable,
			types.ErrOptionWithSkipRetry())
		apiErr.RetryAfterUnix = time.Now().Add(billingUnavailableRetryAfter).Unix()
		return apiErr
	}
}

// platformPreAuthorize calls the platform to freeze wallet balance.
// High-balance users can skip this call entirely (cache-based trust).
func platformPreAuthorize(c *gin.Context, estimatedQuota int, relayInfo *relaycommon.RelayInfo) *types.NewAPIError {
	estimatedLB := currency.QuotaToCNY(estimatedQuota)
	accountID := relayInfo.IdentityAccountID

	// Fast path: skip pre-auth for users with high cached balance.
	// They'll still be charged via settle; this just avoids the synchronous call.
	if common.ShouldSkipPreAuth(accountID, estimatedLB) {
		logger.LogInfo(c, fmt.Sprintf("skipping pre-auth for high-balance account %d (estimated %.4f LB)",
			accountID, estimatedLB))
		relayInfo.PlatformGoverned = true
		return nil
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	preAuthStart := time.Now()
	result, err := preAuthorizeWithBreaker(ctx, accountID, estimatedLB,
		sourceProductOf(relayInfo), "preauth:"+uuid.NewString(), fmt.Sprintf("relay userId=%d model=%s", relayInfo.UserId, relayInfo.OriginModelName), int(PreAuthHoldTTL.Seconds()))
	metrics.BillingPreAuthDuration.Observe(time.Since(preAuthStart).Seconds())

	if err != nil {
		// P1-2: when the platform breaker is OPEN, fall back to cached wallet
		// balance instead of a hard refusal — a billing outage must degrade, not
		// take the gateway down with it. TryDegradedPreAuth is fail-closed and
		// bounded (fresh cache + 3× margin + per-tenant unsecured-spend cap); on
		// success we proceed WITHOUT a pre-auth, taking the same legacy
		// post-consume debit path as the high-balance skip above. This binding
		// is also why the deep readiness probe (P0-2) is safe: /api/health no
		// longer 503s on a billing blip, only on a true DB-down.
		if common.TryDegradedPreAuth(c.GetString("tenant_id"), accountID, estimatedLB, err) {
			logger.LogInfo(c, fmt.Sprintf("billing degraded: admitting account %d on cached balance (estimate %.4f LB, breaker open)",
				accountID, estimatedLB))
			relayInfo.PlatformGoverned = true
			return nil
		}
		return preAuthFailure(err)
	}

	relayInfo.PlatformPreAuthID = result.PreAuthID
	relayInfo.PlatformGoverned = true
	logger.LogInfo(c, fmt.Sprintf("platform pre-auth created: id=%d amount=%.4f LB account=%d",
		result.PreAuthID, estimatedLB, accountID))

	// Warm the wallet cache the degrade path reads. Nothing else writes it, and
	// TryDegradedPreAuth treats an absent entry as "don't trust" — so without
	// this a billing outage denies every relay instead of degrading. A pre-auth
	// that just succeeded is the one moment we know the platform is answering
	// for this account. Claim-gated (≤1 call per account per cache TTL) and off
	// the hot path, with its own context: a refresh must never fail the request.
	if common.ClaimWalletBalanceRefresh(accountID) {
		AsyncGo(func() {
			refreshCtx, refreshCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer refreshCancel()
			common.RefreshCachedWalletBalance(refreshCtx, accountID)
		})
	}
	return nil
}
