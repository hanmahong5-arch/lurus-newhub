package common

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
)

const (
	// walletCachePrefix is the Redis key prefix for cached wallet available balance.
	walletCachePrefix = "wallet:avail:"
	// walletRefreshPrefix keys the single-flight claim that guards the on-miss
	// refresh: without it every concurrent relay for one account fires its own
	// balance call the moment the entry expires.
	walletRefreshPrefix = "wallet:refresh:"
	// walletCacheTTL is how long a cached balance is trusted (30 seconds).
	walletCacheTTL = 30 * time.Second
	// walletTrustThreshold is the minimum available balance (in LB) to skip pre-auth.
	// Users with balance above this threshold get fast-path (no pre-auth call).
	walletTrustThreshold = 10.0 // 10 LB — well above typical single-request cost
)

// GetCachedWalletBalance returns the cached available wallet balance for an account.
// Returns (balance, true) if cached, or (0, false) if not cached or Redis unavailable.
func GetCachedWalletBalance(accountID int64) (float64, bool) {
	if !RedisEnabled || RDB == nil {
		return 0, false
	}
	key := fmt.Sprintf("%s%d", walletCachePrefix, accountID)
	val, err := RDB.Get(context.Background(), key).Result()
	if err != nil {
		metrics.RecordCacheHit("wallet_balance", false)
		return 0, false
	}
	balance, err := strconv.ParseFloat(val, 64)
	if err != nil {
		metrics.RecordCacheHit("wallet_balance", false)
		return 0, false
	}
	metrics.RecordCacheHit("wallet_balance", true)
	return balance, true
}

// SetCachedWalletBalance updates the cached wallet balance after a successful pre-auth or settle.
func SetCachedWalletBalance(accountID int64, balance float64) {
	setCachedWalletBalance(context.Background(), accountID, balance)
}

// setCachedWalletBalance is the context-carrying form. Callers that already hold
// a request or refresh context must use it so the write inherits that deadline
// instead of silently outliving it.
func setCachedWalletBalance(ctx context.Context, accountID int64, balance float64) {
	if !RedisEnabled || RDB == nil {
		return
	}
	key := fmt.Sprintf("%s%d", walletCachePrefix, accountID)
	RDB.Set(ctx, key, fmt.Sprintf("%.4f", balance), walletCacheTTL)
}

// ClaimWalletBalanceRefresh reports whether the caller should go fetch a fresh
// balance for this account: true only when nothing is cached AND no other
// in-flight refresh already claimed the slot. The claim expires with
// walletCacheTTL, so refreshing costs at most one platform call per account per
// TTL however much traffic that account pushes.
func ClaimWalletBalanceRefresh(accountID int64) bool {
	if !RedisEnabled || RDB == nil {
		return false
	}
	ctx := context.Background()
	// Deliberately not GetCachedWalletBalance: this probe must not skew the
	// wallet_balance hit/miss ratio the read path reports.
	if n, err := RDB.Exists(ctx, fmt.Sprintf("%s%d", walletCachePrefix, accountID)).Result(); err != nil || n > 0 {
		return false
	}
	claimed, err := RDB.SetNX(ctx, fmt.Sprintf("%s%d", walletRefreshPrefix, accountID), "1", walletCacheTTL).Result()
	return err == nil && claimed
}

// RefreshCachedWalletBalance caches the AVAILABLE balance (balance minus the
// amount frozen by in-flight pre-auths), which is the quantity every reader
// here compares against a request estimate — caching the gross balance instead
// would vouch for money that is already held. Best effort: on any failure the
// entry stays absent, and an absent entry means no trust and no degradation.
func RefreshCachedWalletBalance(ctx context.Context, accountID int64) {
	wb, err := GetWalletBalance(ctx, accountID)
	if err != nil || wb == nil {
		return
	}
	setCachedWalletBalance(ctx, accountID, wb.Balance-wb.Frozen)
}

// InvalidateCachedWalletBalance removes the cached balance (e.g., after settle or topup).
func InvalidateCachedWalletBalance(accountID int64) {
	if !RedisEnabled || RDB == nil {
		return
	}
	key := fmt.Sprintf("%s%d", walletCachePrefix, accountID)
	RDB.Del(context.Background(), key)
}

// ShouldSkipPreAuth returns true if the user has a high cached balance,
// meaning we can trust them to pay and skip the synchronous pre-auth call.
// This reduces latency for premium users who rarely exhaust their balance.
//
// Off by default, and that default is not conservatism for its own sake: until
// the wallet cache gained a writer this function could never return true, so "every
// relay takes the synchronous freeze" is the behaviour production has actually
// been running and the behaviour settle reconciliation has been observed
// against. Skipping the freeze means the spend is not held, so concurrent
// requests inside the cache TTL can overshoot the balance and are only squared
// up post-consume. Turning that on is a money-path policy decision, so it is an
// operator switch rather than a side effect of caching a balance for the
// outage-degradation path (TryDegradedPreAuth), which reads the same entry.
func ShouldSkipPreAuth(accountID int64, estimatedLB float64) bool {
	if !GetEnvOrDefaultBool("BILLING_WALLET_TRUST_SKIP_PREAUTH", false) {
		return false
	}
	balance, ok := GetCachedWalletBalance(accountID)
	if !ok {
		return false
	}
	return balance > walletTrustThreshold && balance > estimatedLB*3
}

// TryDegradedPreAuth decides whether a relay may proceed on cached wallet
// balance while the platform billing breaker is OPEN (P1-2 graceful
// degradation). Default is FAIL-CLOSED: it returns true only when every guard
// holds, so a billing outage degrades to "charge from cache, reconcile later"
// for trusted balances rather than a blanket 402 that takes the gateway down
// with the platform. Guards:
//   - the breaker is actually OPEN (not a balance rejection or a healthy path),
//   - the pre-auth error is NOT insufficient_balance (never degrade a real
//     "out of money" — that would hand out free spend),
//   - a fresh cached balance exists (TTL-bounded; absent cache ⇒ no degrade),
//   - the estimate sits comfortably under that balance (same 3× margin as the
//     skip-preauth fast path),
//   - admitting this estimate keeps the tenant under its rolling unsecured-spend
//     cap (reserveDegradedSpend).
// The admitted request then takes the existing no-pre-auth post-consume path
// (legacy wallet debit), identical to the high-balance ShouldSkipPreAuth path —
// so this introduces no new settlement gap. Metric: billing_degraded_total.
func TryDegradedPreAuth(tenantID string, accountID int64, estimatedLB float64, preAuthErr error) bool {
	deny := func() bool {
		metrics.BillingDegradedTotal.WithLabelValues("denied").Inc()
		return false
	}
	balance, haveFreshCache := GetCachedWalletBalance(accountID)
	// Pure policy first (exhaustively unit-tested), then the spend-cap reservation
	// (Redis side effect) only once the policy says yes.
	if !degradeAdmissible(BillingBreakerIsOpen(), haveFreshCache, balance, estimatedLB, preAuthErr) {
		return deny()
	}
	if !reserveDegradedSpend(tenantID, estimatedLB) {
		return deny()
	}
	metrics.BillingDegradedTotal.WithLabelValues("allowed").Inc()
	return true
}

// degradeAdmissible is the PURE degrade policy, separated from all I/O (breaker
// read, cache read, cap reservation) so the safety-critical decision is fully
// unit-testable without Redis. Returns true only when EVERY guard holds — the
// fail-closed default lives here. The caller still applies the per-tenant spend
// cap on top before admitting.
func degradeAdmissible(breakerOpen, haveFreshCache bool, balance, estimatedLB float64, preAuthErr error) bool {
	if !breakerOpen {
		return false // platform healthy ⇒ never skip pre-auth via this path
	}
	if preAuthErr != nil && strings.Contains(preAuthErr.Error(), "insufficient_balance") {
		return false // genuine "out of money" must fail closed, never degrade
	}
	if !haveFreshCache {
		return false // no cached balance to trust
	}
	// Same trust margin as the high-balance skip-preauth fast path: balance must
	// clear the floor AND comfortably (3×) exceed this request's estimate.
	return balance > walletTrustThreshold && balance > estimatedLB*3
}

// reserveDegradedSpend atomically reserves estimatedLB against the tenant's
// rolling degraded-spend cap. Returns false (fail-closed) when Redis is absent —
// without a shared counter the unsecured spend can't be bounded, and unbounded
// degradation is exactly what the cap exists to prevent. Worst-case overshoot is
// one request's estimate (check-then-incr is not a single round-trip), which is
// acceptable for a safety net.
func reserveDegradedSpend(tenantID string, estimatedLB float64) bool {
	if !RedisEnabled || RDB == nil {
		return false
	}
	ctx := context.Background()
	key := fmt.Sprintf("billing:degraded:%s", tenantID)
	cur, _ := RDB.Get(ctx, key).Float64() // missing key ⇒ 0
	if cur >= BillingDegradedSpendCapLB {
		return false
	}
	newVal, err := RDB.IncrByFloat(ctx, key, estimatedLB).Result()
	if err != nil {
		return false
	}
	// Stamp the rolling-window TTL on the first write of a fresh window.
	if newVal == estimatedLB {
		RDB.Expire(ctx, key, time.Duration(BillingDegradedWindowSec)*time.Second)
	}
	return true
}
