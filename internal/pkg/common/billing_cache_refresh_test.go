package common

// billing_cache_refresh_test.go — the wallet cache is the sole input to the
// billing-outage degrade decision, so these cover the two properties that
// decide money: the refresh caches AVAILABLE (not gross) balance, and it is
// claim-gated so a cold cache under load costs one platform call, not one per
// request.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// serveWalletBalance points IdentityServiceURL at a stub platform that answers
// the wallet-balance endpoint, and returns the hit counter.
func serveWalletBalance(t *testing.T, body string) *int32 {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	prevURL := IdentityServiceURL
	IdentityServiceURL = srv.URL
	t.Cleanup(func() {
		IdentityServiceURL = prevURL
		srv.Close()
	})
	return &calls
}

// TestRefreshCachedWalletBalance_CachesAvailableNotGross pins the unit the
// degrade guards compare against a request estimate: money already frozen by
// in-flight pre-auths is NOT spendable, so caching the gross balance would
// vouch for it twice.
func TestRefreshCachedWalletBalance_CachesAvailableNotGross(t *testing.T) {
	withMiniRedis(t)
	calls := serveWalletBalance(t, `{"balance":100,"frozen":40}`)

	const acct int64 = 5150
	RefreshCachedWalletBalance(context.Background(), acct)

	if got := atomic.LoadInt32(calls); got != 1 {
		t.Fatalf("platform calls = %d, want 1", got)
	}
	bal, ok := GetCachedWalletBalance(acct)
	if !ok {
		t.Fatal("refresh must populate the cache")
	}
	if bal != 60 {
		t.Errorf("cached balance = %v, want 60 (100 balance − 40 frozen)", bal)
	}
}

// TestRefreshCachedWalletBalance_PlatformDownLeavesCacheCold — a failed refresh
// must leave nothing behind, because an absent entry is what makes the degrade
// path fail closed.
func TestRefreshCachedWalletBalance_PlatformDownLeavesCacheCold(t *testing.T) {
	withMiniRedis(t)
	prevURL := IdentityServiceURL
	IdentityServiceURL = "" // unconfigured platform ⇒ GetWalletBalance returns nil
	t.Cleanup(func() { IdentityServiceURL = prevURL })

	const acct int64 = 5152
	RefreshCachedWalletBalance(context.Background(), acct)

	if _, ok := GetCachedWalletBalance(acct); ok {
		t.Error("a failed refresh must not cache anything")
	}
}

// TestClaimWalletBalanceRefresh_SingleFlightAndWarmSuppression bounds the cost:
// only the first caller on a cold cache goes to the platform, and once an entry
// exists nobody does.
func TestClaimWalletBalanceRefresh_SingleFlightAndWarmSuppression(t *testing.T) {
	withMiniRedis(t)
	const acct int64 = 5151

	if !ClaimWalletBalanceRefresh(acct) {
		t.Fatal("cold cache must hand out the refresh claim")
	}
	if ClaimWalletBalanceRefresh(acct) {
		t.Error("a concurrent caller must not fire a second refresh")
	}

	// Entry now present: no refresh even after the claim marker is gone.
	SetCachedWalletBalance(acct, 42)
	RDB.Del(context.Background(), fmt.Sprintf("%s%d", walletRefreshPrefix, acct))
	if ClaimWalletBalanceRefresh(acct) {
		t.Error("warm cache must not trigger a refresh")
	}
}

// TestClaimWalletBalanceRefresh_NoRedis — without Redis there is nowhere to
// cache, so the claim is refused rather than spending a platform call.
func TestClaimWalletBalanceRefresh_NoRedis(t *testing.T) {
	prevRDB, prevEnabled := RDB, RedisEnabled
	RDB, RedisEnabled = nil, false
	t.Cleanup(func() { RDB, RedisEnabled = prevRDB, prevEnabled })

	if ClaimWalletBalanceRefresh(5153) {
		t.Error("no Redis ⇒ no claim")
	}
}

// TestTryDegradedPreAuth_AdmitsOnlyAfterRefresh is the defect in miniature: an
// unpopulated cache denies every relay while billing is down (blanket 402), and
// one refresh flips that to the intended degraded admission — without ever
// admitting a request the cached balance cannot cover.
func TestTryDegradedPreAuth_AdmitsOnlyAfterRefresh(t *testing.T) {
	withMiniRedis(t)
	resetBillingBreaker(t)
	openBillingBreaker(t)
	serveWalletBalance(t, `{"balance":100,"frozen":40}`) // available 60

	const (
		acct   int64 = 5154
		tenant       = "t-refresh"
	)
	prevCap, prevWin := BillingDegradedSpendCapLB, BillingDegradedWindowSec
	BillingDegradedSpendCapLB, BillingDegradedWindowSec = 100.0, 3600
	t.Cleanup(func() { BillingDegradedSpendCapLB, BillingDegradedWindowSec = prevCap, prevWin })

	if TryDegradedPreAuth(tenant, acct, 1.0, errBreakerOpen()) {
		t.Fatal("precondition: cold cache must deny")
	}

	RefreshCachedWalletBalance(context.Background(), acct)

	if !TryDegradedPreAuth(tenant, acct, 1.0, errBreakerOpen()) {
		t.Error("warm cache + breaker open must admit a request well inside the balance")
	}
	// The margin is not a formality: 30 LB is under the gross 100 but over the
	// 60 actually available (and over the 3× trust margin), so it stays denied.
	if TryDegradedPreAuth(tenant, acct, 30.0, errBreakerOpen()) {
		t.Error("an over-budget estimate must still be denied on a warm cache")
	}
}
