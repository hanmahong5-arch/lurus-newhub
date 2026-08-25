package app

// pre_consume_wallet_cache_test.go — a successful platform pre-auth is the only
// production writer of the wallet-balance cache, and that cache is the only
// input TryDegradedPreAuth trusts. If the success path stops warming it, a
// billing outage stops degrading and starts returning a blanket 402 to every
// relay, so the warm-up is asserted here rather than left implicit.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// stubPlatformPreAuth makes the freeze call succeed without a network round
// trip (the real gRPC client waits out the whole request deadline in a test
// binary) and returns the accountID it was asked to freeze against.
func stubPlatformPreAuth(t *testing.T, preAuthID int64) {
	t.Helper()
	prev := preAuthorizeWithBreaker
	preAuthorizeWithBreaker = func(ctx context.Context, accountID int64, amount float64,
		productID, referenceID, description string, ttlSeconds int) (*common.PreAuthResult, error) {
		return &common.PreAuthResult{PreAuthID: preAuthID, Amount: amount, Status: "held"}, nil
	}
	t.Cleanup(func() { preAuthorizeWithBreaker = prev })
}

// syncAsyncGo runs AsyncGo work inline-ish and returns a wait func, so the
// fire-and-forget refresh cannot outlive the test.
func syncAsyncGo(t *testing.T) func() {
	t.Helper()
	var wg sync.WaitGroup
	prev := AsyncGo
	AsyncGo = func(f func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f()
		}()
	}
	t.Cleanup(func() { AsyncGo = prev })
	return wg.Wait
}

func TestPlatformPreAuthorize_WarmsWalletCacheForDegradedAdmission(t *testing.T) {
	withMiniRedisTPM(t)
	stubPlatformPreAuth(t, 777)
	wait := syncAsyncGo(t)

	const acct int64 = 66021
	// Platform reports 100 held, 40 of it frozen ⇒ 60 spendable.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/wallet/balance") {
			t.Errorf("unexpected platform call: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"balance": 100.0, "frozen": 40.0})
	}))
	defer srv.Close()
	prevURL := common.IdentityServiceURL
	common.IdentityServiceURL = srv.URL
	t.Cleanup(func() { common.IdentityServiceURL = prevURL })

	c := createTestGinContext()
	c.Request = httptest.NewRequest(http.MethodPost, "/relay", nil)
	c.Set("tenant_id", "t-warm")
	relayInfo := &relaycommon.RelayInfo{
		UserId:            4321,
		IdentityAccountID: acct,
		OriginModelName:   "gpt-4",
	}

	if apiErr := platformPreAuthorize(c, 1_000, relayInfo); apiErr != nil {
		t.Fatalf("platformPreAuthorize: %v", apiErr.Error())
	}
	if relayInfo.PlatformPreAuthID != 777 {
		t.Fatalf("PlatformPreAuthID = %d, want 777", relayInfo.PlatformPreAuthID)
	}
	wait()

	bal, ok := common.GetCachedWalletBalance(acct)
	if !ok {
		t.Fatal("a successful pre-auth must leave the wallet cache warm — otherwise the degrade path can never fire")
	}
	if bal != 60 {
		t.Errorf("cached balance = %v, want 60 (available, i.e. 100 − 40 frozen)", bal)
	}

	// With that cache warm, a billing outage now degrades instead of 402-ing.
	common.BillingBreakerFailure()
	common.BillingBreakerFailure()
	common.BillingBreakerFailure()
	if !common.BillingBreakerIsOpen() {
		t.Fatal("precondition: breaker should be OPEN after 3 failures")
	}
	t.Cleanup(common.BillingBreakerSuccess)

	prevCap := common.BillingDegradedSpendCapLB
	common.BillingDegradedSpendCapLB = 100.0
	t.Cleanup(func() { common.BillingDegradedSpendCapLB = prevCap })

	outage := errPlatformDown()
	if !common.TryDegradedPreAuth("t-warm", acct, 1.0, outage) {
		t.Error("warm cache + open breaker must admit a request well inside the balance")
	}
	// The guard still binds: 30 LB fits under the gross 100 but not under the
	// 60 actually available, so it must stay denied.
	if common.TryDegradedPreAuth("t-warm", acct, 30.0, outage) {
		t.Error("an over-budget estimate must be denied even on a warm cache")
	}
}

// errPlatformDown is any non-insufficient_balance pre-auth error (the only kind
// the degrade path is allowed to consider).
func errPlatformDown() error { return context.DeadlineExceeded }
