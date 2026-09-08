package app

// wallet_charge_metric_test.go — the settle leg of PostConsumeQuota's platform
// pre-auth branch (quota.go, Phase 5) used to leave billing_debit_amount_cny
// silent: only the direct DebitWalletGRPC call sites (the legacy no-pre-auth
// branch) observed it, so a request that settled through the wallet pre-auth
// path — the pre-auth → settle path — moved real money and produced
// zero histogram observations no matter how much. Shape follows
// a2_legacy_debit_gate_test.go: a swappable seam over the money-moving call
// (settleWithBreaker), stubbed to succeed without a network call, so the
// observation itself — not its eventual delivery to a real platform — is
// what this test proves.

import (
	"context"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// hookSettleSeam stubs the settle call so the test never dials a real
// platform. Mirrors hookLegacyDebit's shape (a2_legacy_debit_gate_test.go)
// for the pre-auth branch.
func hookSettleSeam(t *testing.T) {
	t.Helper()
	prev := settleWithBreaker
	settleWithBreaker = func(_ context.Context, preAuthID int64, _ float64) (*common.SettlePreAuthResult, error) {
		return &common.SettlePreAuthResult{PreAuthID: preAuthID, Status: "settled"}, nil
	}
	t.Cleanup(func() { settleWithBreaker = prev })
}

// billingDebitObservations reads the cumulative sample_count for one
// (product, op) series of the billing_debit_amount_cny histogram.
// HistogramVec.WithLabelValues returns an Observer, whose concrete type also
// implements prometheus.Metric — the same pattern as
// internal/pkg/metrics/metrics_helpers_test.go's observerSampleCount, which
// this package cannot import (it lives in another package's _test.go).
func billingDebitObservations(t *testing.T, product, op string) int {
	t.Helper()
	metric, ok := metrics.BillingDebitAmountCNY.WithLabelValues(product, op).(prometheus.Metric)
	if !ok {
		t.Fatalf("BillingDebitAmountCNY observer does not implement prometheus.Metric")
	}
	var m dto.Metric
	if err := metric.Write(&m); err != nil {
		t.Fatalf("write metric: %v", err)
	}
	hist := m.GetHistogram()
	if hist == nil {
		t.Fatalf("metric is not a histogram")
	}
	return int(hist.GetSampleCount())
}

// TestPostConsumeQuota_PreAuthSettle_ObservesBillingDebitMetric is the oracle
// for the settle-leg metric: a request that settles through the platform
// wallet pre-auth branch must land one observation on
// billing_debit_amount_cny{product,op="settle"}.
func TestPostConsumeQuota_PreAuthSettle_ObservesBillingDebitMetric(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	isolateBizTPMWindow(t)
	legacyDebitEnv(t)
	hookSettleSeam(t)

	prevAsync := AsyncGo
	AsyncGo = func(f func()) { f() }
	t.Cleanup(func() { AsyncGo = prevAsync })

	userId := seedTestUser(t, db, 100_000)
	key, tokenId := seedTestToken(t, db, userId, 100_000, false)

	relayInfo := &relaycommon.RelayInfo{
		UserId:            userId,
		TokenId:           tokenId,
		TokenKey:          key,
		IdentityAccountID: 77,
		PlatformPreAuthID: 42,
		SourceProduct:     "switch",
	}

	before := billingDebitObservations(t, "switch", "settle")
	if err := PostConsumeQuota(relayInfo, 700, 0, false); err != nil {
		t.Fatalf("PostConsumeQuota: %v", err)
	}
	after := billingDebitObservations(t, "switch", "settle")

	if delta := after - before; delta != 1 {
		t.Errorf("billing_debit_amount_cny{product=switch,op=settle} delta = %d, want 1", delta)
	}

	// The debit-op series must be untouched by a settle — the two legs stay
	// distinguishable, never merged into one undifferentiated counter.
	if got := billingDebitObservations(t, "switch", "debit"); got != 0 {
		t.Errorf("billing_debit_amount_cny{product=switch,op=debit} = %d, want 0 (this request settled, did not debit)", got)
	}
}
