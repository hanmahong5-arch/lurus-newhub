package app

// billing_money_lost_test.go — the three places a wallet movement can fall
// through BOTH its live call and the outbox used to say so only in a
// free-text SysError line. The 2026-09-23 credit-pool incident (4.7h of 402s
// with nothing on any dashboard) is what a log-only failure signal looks
// like in practice; CreditPoolDebitLostTotal closed it for the pool path and
// left the wallet path uncovered. Each test here drives one site with the
// outbox uninitialised (billingOutboxDB == nil makes every Enqueue* fail,
// alpha7_coverage_test.go) and asserts the stage's series moved by exactly 1.

import (
	"context"
	"errors"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func moneyLost(stage string) float64 {
	return testutil.ToFloat64(metrics.BillingMoneyPathLostTotal.WithLabelValues(stage))
}

// withoutOutbox makes every Enqueue* fail, the way it does on a pod whose
// outbox never initialised, and pins the breaker so the failing calls here
// cannot open it for the tests that follow.
func withoutOutbox(t *testing.T) {
	t.Helper()
	prev := billingOutboxDB
	billingOutboxDB = nil
	t.Cleanup(func() { billingOutboxDB = prev })
	legacyDebitEnv(t)
	common.BillingBreakerSuccess()
	t.Cleanup(common.BillingBreakerSuccess)
}

func TestMoneyLost_SettleFailedAndOutboxFailed_CountsSettleStage(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	isolateBizTPMWindow(t)
	withoutOutbox(t)

	prevSettle := settleWithBreaker
	settleWithBreaker = func(context.Context, int64, float64) (*common.SettlePreAuthResult, error) {
		return nil, errors.New("platform unavailable")
	}
	t.Cleanup(func() { settleWithBreaker = prevSettle })

	prevAsync := AsyncGo
	AsyncGo = func(f func()) { f() }
	t.Cleanup(func() { AsyncGo = prevAsync })

	userId := seedTestUser(t, db, 100_000)
	key, tokenId := seedTestToken(t, db, userId, 100_000, false)
	relayInfo := &relaycommon.RelayInfo{
		UserId: userId, TokenId: tokenId, TokenKey: key,
		IdentityAccountID: 77, PlatformPreAuthID: 42,
	}

	before := moneyLost("settle")
	if err := PostConsumeQuota(relayInfo, 700, 0, false); err != nil {
		t.Fatalf("PostConsumeQuota: %v", err)
	}
	if delta := moneyLost("settle") - before; delta != 1 {
		t.Errorf("lurus_billing_money_path_lost_total{stage=settle} delta = %v, want 1", delta)
	}
	if relayInfo.PlatformPreAuthID != 0 {
		t.Errorf("PlatformPreAuthID = %d after settlement, want 0 (handled, even if lost)", relayInfo.PlatformPreAuthID)
	}
}

func TestMoneyLost_ReleaseFailedAndOutboxFailed_CountsReleaseStage(t *testing.T) {
	withoutOutbox(t)
	// IdentityServiceURL is "" (legacyDebitEnv), so the release call itself
	// fails without dialing anything.
	relayInfo := &relaycommon.RelayInfo{IdentityAccountID: 77, PlatformPreAuthID: 43}

	before := moneyLost("preauth_release")
	abandonPreAuth(relayInfo, "test")
	if delta := moneyLost("preauth_release") - before; delta != 1 {
		t.Errorf("lurus_billing_money_path_lost_total{stage=preauth_release} delta = %v, want 1", delta)
	}
	if relayInfo.PlatformPreAuthID != 0 {
		t.Errorf("PlatformPreAuthID = %d after abandon, want 0", relayInfo.PlatformPreAuthID)
	}
}

func TestMoneyLost_DebitFailedAndOutboxFailed_CountsDebitStage(t *testing.T) {
	withoutOutbox(t)

	before := moneyLost("debit")
	enqueueFailedLegacyDebit(77, 1.5, "relay userId=1", "switch", "llm-usage:test", errors.New("platform unavailable"))
	if delta := moneyLost("debit") - before; delta != 1 {
		t.Errorf("lurus_billing_money_path_lost_total{stage=debit} delta = %v, want 1", delta)
	}
}

// A parked movement is not a lost one: with the outbox up, none of the three
// stages may count.
func TestMoneyLost_OutboxUp_NothingCounted(t *testing.T) {
	db := setupServiceTestDB(t)
	if err := InitBillingOutbox(db); err != nil {
		t.Fatalf("init outbox: %v", err)
	}
	prev := billingOutboxDB
	t.Cleanup(func() { billingOutboxDB = prev })
	legacyDebitEnv(t)
	common.BillingBreakerSuccess()
	t.Cleanup(common.BillingBreakerSuccess)

	before := moneyLost("preauth_release") + moneyLost("debit")
	abandonPreAuth(&relaycommon.RelayInfo{IdentityAccountID: 77, PlatformPreAuthID: 44}, "test")
	enqueueFailedLegacyDebit(77, 1.5, "relay userId=1", "switch", "llm-usage:test-parked", errors.New("platform unavailable"))
	if delta := moneyLost("preauth_release") + moneyLost("debit") - before; delta != 0 {
		t.Errorf("money_path_lost moved by %v although both movements were parked in the outbox", delta)
	}
}
