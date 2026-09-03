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
	releasePlatformPreAuth(relayInfo)
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
			// Both release and outbox failed — platform TTL (300s) is the safety net.
			// Log at highest severity so ops can investigate.
			common.SysError(fmt.Sprintf("CRITICAL: pre-auth %d stuck frozen — both release and outbox failed. "+
				"Platform TTL will auto-expire in ≤300s. release_err=%s, outbox_err=%s",
				preAuthID, err.Error(), enqErr.Error()))
		}
	}
	// Keep PlatformPreAuthID for observability in logs/metrics (don't clear to 0).
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
				// Local quota insufficient — must release platform pre-auth if one was created.
				releasePlatformPreAuth(relayInfo)
				relayInfo.PlatformPreAuthID = 0
				return types.NewErrorWithStatusCode(
					fmt.Errorf("insufficient quota: available %s, required %s",
						logger.FormatQuota(userQuota), logger.FormatQuota(preConsumedQuota)),
					types.ErrorCodeInsufficientUserQuota, http.StatusPaymentRequired,
					types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog(),
					types.ErrOptionWithTopupURL())
			}
		}
	}

	// Tenant monthly quota enforcement (runs after user-level check).
	if apiErr := enforceTenantQuota(c.GetString("tenant_id"), preConsumedQuota); apiErr != nil {
		releasePlatformPreAuth(relayInfo)
		relayInfo.PlatformPreAuthID = 0
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
				releasePlatformPreAuth(relayInfo)
				relayInfo.PlatformPreAuthID = 0
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
					if compErr := repo.IncreaseTokenQuota(relayInfo.TokenId, relayInfo.TokenKey, preConsumedQuota); compErr != nil {
						common.SysError(fmt.Sprintf("advisory: token freeze rollback failed: tokenId=%d, quota=%d, err=%s",
							relayInfo.TokenId, preConsumedQuota, compErr.Error()))
					}
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
					if compErr := repo.IncreaseTokenQuota(relayInfo.TokenId, relayInfo.TokenKey, preConsumedQuota); compErr != nil {
						common.SysError(fmt.Sprintf("token freeze rollback failed: tokenId=%d, quota=%d, err=%s",
							relayInfo.TokenId, preConsumedQuota, compErr.Error()))
					}
					releasePlatformPreAuth(relayInfo)
					relayInfo.PlatformPreAuthID = 0
					if err != nil {
						// Real DB error — same error code/path as before.
						return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
					}
					// ok == false: balance out-raced the pre-check → the same 402 the
					// fast path returns for insufficient local quota.
					return types.NewErrorWithStatusCode(
						fmt.Errorf("insufficient quota: available %s, required %s",
							logger.FormatQuota(userQuota), logger.FormatQuota(preConsumedQuota)),
						types.ErrorCodeInsufficientUserQuota, http.StatusPaymentRequired,
						types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog(),
						types.ErrOptionWithTopupURL())
				}
			}
		}
	}

	relayInfo.FinalPreConsumedQuota = preConsumedQuota
	return nil
}

// preAuthorizeWithBreaker is the platform freeze call. A var (same seam
// convention as AsyncGo) because the identity gRPC client dials with
// WaitForReady, so in a test binary the call burns the whole request deadline
// before falling back to HTTP — which leaves the success path below, and the
// cache warm-up that hangs off it, otherwise unreachable from tests.
var preAuthorizeWithBreaker = common.PreAuthorizeWithBreaker

// platformPreAuthorize calls the platform to freeze wallet balance.
// High-balance users can skip this call entirely (cache-based trust).
func platformPreAuthorize(c *gin.Context, estimatedQuota int, relayInfo *relaycommon.RelayInfo) *types.NewAPIError {
	estimatedLB := float64(estimatedQuota) / common.QuotaPerUnit
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
		sourceProductOf(relayInfo), "preauth:"+uuid.NewString(), fmt.Sprintf("relay userId=%d model=%s", relayInfo.UserId, relayInfo.OriginModelName), 300)
	metrics.BillingPreAuthDuration.Observe(time.Since(preAuthStart).Seconds())

	if err != nil {
		// P1-2: when the platform breaker is OPEN, fall back to cached wallet
		// balance instead of a hard 402 — a billing outage must degrade, not take
		// the gateway down with it. TryDegradedPreAuth is fail-closed and bounded
		// (fresh cache + 3× margin + per-tenant unsecured-spend cap); on success
		// we proceed WITHOUT a pre-auth, taking the same legacy post-consume debit
		// path as the high-balance skip above. This binding is also why the deep
		// readiness probe (P0-2) is safe: /api/health no longer 503s on a billing
		// blip, only on a true DB-down.
		if common.TryDegradedPreAuth(c.GetString("tenant_id"), accountID, estimatedLB, err) {
			logger.LogInfo(c, fmt.Sprintf("billing degraded: admitting account %d on cached balance (estimate %.4f LB, breaker open)",
				accountID, estimatedLB))
			relayInfo.PlatformGoverned = true
			return nil
		}
		return types.NewErrorWithStatusCode(
			fmt.Errorf("insufficient balance or billing service unavailable"),
			types.ErrorCodeInsufficientUserQuota, http.StatusPaymentRequired,
			types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog(),
			types.ErrOptionWithTopupURL())
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
